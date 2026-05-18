package utils

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestFloat_ScalesByDecimals(t *testing.T) {
	info := &ERC20Info{Balance: big.NewInt(1234567), Decimals: 6}
	got := info.Float()
	if got != 1.234567 {
		t.Fatalf("got %v", got)
	}
}

func TestFloat_ZeroBalance(t *testing.T) {
	info := &ERC20Info{Balance: big.NewInt(0), Decimals: 18}
	if info.Float() != 0 {
		t.Fatalf("expected 0")
	}
}

func TestMulticall_UnsupportedChain(t *testing.T) {
	// chainID 999 is not configured
	_, err := FetchERC20InfoMulticall3(context.Background(), nil, 999,
		common.HexToAddress("0xdeadbeef"), common.HexToAddress("0xcafebabe"))
	if err == nil {
		t.Fatalf("expected error for unsupported chain")
	}
}

func TestMulticall_UnsupportedChain_ChainIDZero(t *testing.T) {
	// chainID 0 is not in the multicall3 map either.
	_, err := FetchERC20InfoMulticall3(context.Background(), nil, 0,
		common.HexToAddress("0xdeadbeef"), common.HexToAddress("0xcafebabe"))
	if err == nil {
		t.Fatalf("expected error for chainID=0")
	}
}

func TestFloat_DifferentDecimals(t *testing.T) {
	for _, dec := range []uint8{6, 8, 18} {
		info := &ERC20Info{Balance: big.NewInt(1_000_000), Decimals: dec}
		got := info.Float()
		if got <= 0 {
			t.Fatalf("dec=%d got %v", dec, got)
		}
	}
}

func TestFloat_LargeBalance(t *testing.T) {
	// 1 * 10^18 with 18 decimals should yield 1.0
	bal, ok := new(big.Int).SetString("1000000000000000000", 10)
	if !ok {
		t.Fatal("bad big.Int literal")
	}
	info := &ERC20Info{Balance: bal, Decimals: 18}
	if got := info.Float(); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestERC20Info_String(t *testing.T) {
	info := &ERC20Info{
		Balance:  big.NewInt(1_500_000),
		Decimals: 6,
		Symbol:   "USDC",
		Token:    common.HexToAddress("0xdeadbeef"),
	}
	s := info.String()
	if s == "" {
		t.Fatal("String() returned empty result")
	}
	if !strings.Contains(s, "USDC") {
		t.Fatalf("expected USDC in string, got %q", s)
	}
	// 1.5 USDC should be present in some textual form (decimals=6 => "1.500000")
	if !strings.Contains(s, "1.500000") {
		t.Fatalf("expected formatted balance in string, got %q", s)
	}
}

func TestERC20Info_String_ZeroSymbol(t *testing.T) {
	info := &ERC20Info{
		Balance:  big.NewInt(0),
		Decimals: 18,
		Symbol:   "",
	}
	s := info.String()
	if s == "" {
		t.Fatal("String() returned empty result for zero balance")
	}
}

// newFakeRPCServer spins up an httptest server that responds to all JSON-RPC
// calls with either an explicit error (when errMsg != "") or with the given
// hex-encoded result. Returns a connected *ethclient.Client and a cleanup func.
func newFakeRPCServer(t *testing.T, errMsg, resultHex string) (*ethclient.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if errMsg != "" {
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"error": map[string]any{
					"code":    -32000,
					"message": errMsg,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result":  resultHex,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	c, err := ethclient.Dial(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	return c, func() {
		c.Close()
		srv.Close()
	}
}

func TestFetchERC20InfoMulticall3_CallContractError(t *testing.T) {
	// Server replies with a JSON-RPC error → ethclient.CallContract returns
	// an error; FetchERC20InfoMulticall3 should propagate it.
	c, stop := newFakeRPCServer(t, "execution reverted", "")
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := FetchERC20InfoMulticall3(ctx, c, 137,
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"))
	if err == nil {
		t.Fatal("expected error from failing CallContract")
	}
}

// encodeAggregate3AllFailReturn constructs an ABI-encoded aggregate3 return
// value for three failed sub-calls. All sub-calls have success=false and empty
// returnData, exercising the `else` branches inside FetchERC20InfoMulticall3
// (balance defaults to 0, decimals defaults to 18, symbol stays empty).
func encodeAggregate3AllFailReturn(t *testing.T) string {
	t.Helper()
	// Manually craft the ABI tuple[] return. Layout:
	//   [0x00..0x20)  offset to tuple[] = 0x20
	//   [0x20..0x40)  length of array = 3
	//   then for each tuple, offset relative to start-of-tuple-data:
	//     offsets: 0x60, 0xc0, 0x120 (3 offsets of 32 bytes each)
	//   then for each tuple body:
	//     - success (32 bytes) = 0
	//     - bytes offset (32 bytes) = 0x40
	//     - bytes length (32 bytes) = 0
	//
	// Each tuple body occupies: 0x60 (3 * 32) bytes.

	hexWord := func(n uint64) string {
		s := ""
		for i := 0; i < 32; i++ {
			b := byte(n >> ((31 - i) * 8))
			s += hex.EncodeToString([]byte{b})
		}
		return s
	}

	// dynamic array offset
	out := hexWord(0x20) // first slot: offset of array
	// array length
	out += hexWord(3)
	// 3 element offsets — relative to start of array data (after length)
	out += hexWord(0x60)  // first tuple offset
	out += hexWord(0xc0)  // second tuple offset
	out += hexWord(0x120) // third tuple offset
	// 3 tuple bodies, each: success=0, bytes-offset=0x40, bytes-length=0
	for i := 0; i < 3; i++ {
		out += hexWord(0)    // success = false
		out += hexWord(0x40) // bytes offset within tuple
		out += hexWord(0)    // bytes length 0
	}
	return "0x" + out
}

func TestFetchERC20InfoMulticall3_AllSubcallsFailed_Defaults(t *testing.T) {
	// Encode a successful aggregate3 reply where every sub-call failed.
	resHex := encodeAggregate3AllFailReturn(t)
	c, stop := newFakeRPCServer(t, "", resHex)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := FetchERC20InfoMulticall3(ctx, c, 137,
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Balance == nil || info.Balance.Sign() != 0 {
		t.Fatalf("expected default Balance=0, got %v", info.Balance)
	}
	if info.Decimals != 18 {
		t.Fatalf("expected default Decimals=18, got %d", info.Decimals)
	}
	if info.Symbol != "" {
		t.Fatalf("expected empty Symbol on failed call, got %q", info.Symbol)
	}
}

func TestFetchERC20InfoMulticall3_UnpackError(t *testing.T) {
	// Server replies success with garbage payload → ABI unpack should fail.
	// Returning literally "0x" gives empty data, which won't satisfy the
	// expected aggregate3 layout.
	c, stop := newFakeRPCServer(t, "", "0x")
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := FetchERC20InfoMulticall3(ctx, c, 1,
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"))
	if err == nil {
		t.Fatal("expected error when aggregate3 reply cannot be unpacked")
	}
}

// hexWord32 returns the right-padded 32-byte hex string for a uint64 value.
func hexWord32(n uint64) string {
	s := ""
	for i := 0; i < 32; i++ {
		b := byte(n >> ((31 - i) * 8))
		s += hex.EncodeToString([]byte{b})
	}
	return s
}

// encodeAggregate3SuccessReturn builds a successful aggregate3 response for
// three sub-calls returning a uint256 balance, uint8 decimals, and a short
// string symbol respectively.
//
// The aggregate3 ABI return is `tuple(bool,bytes)[]`:
//
//	offset(0x20) | length(3) | offset_to_t0 | offset_to_t1 | offset_to_t2 | t0... | t1... | t2...
//
// Each tuple body is `(bool success, bytes returnData)`:
//
//	success | offset_within_tuple(0x40) | bytes_length | bytes_data_padded
func encodeAggregate3SuccessReturn(t *testing.T, balance uint64, decimals uint8, symbol string) string {
	t.Helper()

	// Encode each tuple body individually so we can compute offsets dynamically.
	type tupBytes struct {
		head string // success word + bytes-offset word + bytes-length word
		data string // padded data words (variable-length per tuple)
	}

	encodeTup := func(returnDataHex string) tupBytes {
		// returnDataHex is the raw ABI payload (no length prefix), already
		// padded to 32-byte boundary.
		dataBytes := len(returnDataHex) / 2
		// "head" of a (bool,bytes) tuple consumes 3 * 32 bytes:
		//   - success
		//   - dynamic offset to bytes (always 0x40 from the start of the tuple)
		//   - bytes length
		// followed by the (padded) returnData itself.
		head := hexWord32(1) // success = true
		head += hexWord32(0x40)
		head += hexWord32(uint64(dataBytes))
		return tupBytes{head: head, data: returnDataHex}
	}

	// 1) balanceOf -> uint256 (single 32-byte word)
	balData := hexWord32(balance)
	tup0 := encodeTup(balData)

	// 2) decimals -> uint8 (single 32-byte word, low-byte set)
	decData := hexWord32(uint64(decimals))
	tup1 := encodeTup(decData)

	// 3) symbol -> string. ABI string is `(offset 0x20)(length N)(bytes padded)`.
	// We pre-encode that here.
	symBytes := []byte(symbol)
	symPadded := hex.EncodeToString(symBytes)
	// Pad to 32-byte boundary.
	for len(symPadded)%64 != 0 {
		symPadded += "00"
	}
	symPayload := hexWord32(0x20) + hexWord32(uint64(len(symBytes))) + symPadded
	tup2 := encodeTup(symPayload)

	// Tuple body sizes (head + data) in bytes.
	tup0Bytes := (len(tup0.head) + len(tup0.data)) / 2
	tup1Bytes := (len(tup1.head) + len(tup1.data)) / 2

	// Offsets are measured from the start of the dynamic data (right after
	// the array length word). The three offset slots themselves take 3*32 = 0x60.
	off0 := uint64(0x60)
	off1 := off0 + uint64(tup0Bytes)
	off2 := off1 + uint64(tup1Bytes)

	out := hexWord32(0x20) // top-level offset to tuple[]
	out += hexWord32(3)    // array length
	out += hexWord32(off0)
	out += hexWord32(off1)
	out += hexWord32(off2)
	out += tup0.head + tup0.data
	out += tup1.head + tup1.data
	out += tup2.head + tup2.data
	return "0x" + out
}

func TestFetchERC20InfoMulticall3_HappyPath(t *testing.T) {
	resHex := encodeAggregate3SuccessReturn(t, 1_500_000, 6, "USDC")
	c, stop := newFakeRPCServer(t, "", resHex)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := FetchERC20InfoMulticall3(ctx, c, 137,
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Balance == nil || info.Balance.Uint64() != 1_500_000 {
		t.Fatalf("balance mismatch: %v", info.Balance)
	}
	if info.Decimals != 6 {
		t.Fatalf("decimals mismatch: %d", info.Decimals)
	}
	if info.Symbol != "USDC" {
		t.Fatalf("symbol mismatch: %q", info.Symbol)
	}
	if info.Token != common.HexToAddress("0x1111111111111111111111111111111111111111") {
		t.Fatalf("token address not set: %v", info.Token)
	}
}
