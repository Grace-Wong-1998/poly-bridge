package test

import (
	"fmt"
	"os"
	"poly-swap/coinpricedao"
	"poly-swap/coinpricelisten"
	"poly-swap/conf"
	"testing"
)

func TestListenCoinPrice(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	fmt.Printf("current directory: %s\n", dir)
	config := conf.NewConfig("./../../conf/config_testnet.json")
	if config == nil {
		panic("read config failed!")
	}
	dao := coinpricedao.NewCoinPriceDao(conf.SERVER_STAKE, config.DBConfig)
	if dao == nil {
		panic("server is not valid")
	}
	priceListenConfig := config.CoinPriceListenConfig
	priceMarkets := make([]coinpricelisten.PriceMarket, 0)
	for _, cfg := range priceListenConfig {
		priceMarket := coinpricelisten.NewPriceMarket(cfg)
		priceMarkets = append(priceMarkets, priceMarket)
	}
	cpListen := coinpricelisten.NewCoinPriceListen(config.CoinPriceUpdateSlot, priceMarkets, dao)
	cpListen.ListenPrice()
}