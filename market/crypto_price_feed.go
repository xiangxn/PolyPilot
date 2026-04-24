package market

import (
	"context"
	"polypilot/core"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

const readonlyPrivKey = "1111111111111111111111111111111111111111111111111111111111111111"

type CryptoPriceFeed struct {
	Bus                *core.EventBus
	MonitoSymble       string
	MonitorType        sdk.MonitorType
	Config             *sdk.Config
	cryptoPriceMonitor *sdk.CryptoPriceMonitor
}

func (c *CryptoPriceFeed) Init(bus *core.EventBus) {
	c.Bus = bus

	cfg := c.Config
	if cfg == nil {
		cfg = sdk.DefaultConfig()
	}

	client := sdk.NewClient(cfg)
	c.cryptoPriceMonitor = sdk.NewCryptoPriceMonitor(client, c.MonitorType, c.MonitoSymble)
}

func (c *CryptoPriceFeed) Start(ctx context.Context) {
	if c.Bus == nil {
		return
	}

	priceCh := c.cryptoPriceMonitor.Subscribe()

	go c.cryptoPriceMonitor.Run(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-priceCh:
				// log.Printf("data: %+v", data)
				if ok {
					c.Bus.Publish(core.Event{
						Type: core.EventSignal,
						Data: data,
					})
				}
			}
		}
	}()

}
