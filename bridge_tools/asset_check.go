package main

import (
	"encoding/json"
	"fmt"
	"github.com/polynetwork/poly-io-test/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io/ioutil"
	"math/big"
	"net/http"
	"poly-bridge/basedef"
	"poly-bridge/common"
	"poly-bridge/conf"
	"poly-bridge/models"
	"poly-bridge/utils/decimal"
	"time"
)

type AssetDetail struct {
	BasicName   string
	TokenAsset  []DstChainAsset
	Difference  *big.Int
	Precision   uint64
	Price       int64
	Amount_usd  *big.Int
	Amount_usd1 *big.Int
}
type DstChainAsset struct {
	ChainId     uint64
	TotalSupply *big.Int
	Balance     *big.Int
	flow        *big.Int
}

func startCheckAsset(dbCfg *conf.DBConfig) {
	test()
	return

	log.Info("q-w-e-r-t start startCheckAsset")
	Logger := logger.Default
	if dbCfg.Debug == true {
		Logger = Logger.LogMode(logger.Info)
	}
	db, err := gorm.Open(mysql.Open(dbCfg.User+":"+dbCfg.Password+"@tcp("+dbCfg.URL+")/"+
		dbCfg.Scheme+"?charset=utf8"), &gorm.Config{Logger: Logger})
	if err != nil {
		panic(err)
	}

	resAssetDetails := make([]*AssetDetail, 0)
	extraAssetDetails := make([]*AssetDetail, 0)
	tokenBasics := make([]*models.TokenBasic, 0)
	res := db.
		Where("property = ?", 1).
		Preload("Tokens").
		Find(&tokenBasics)
	if res.Error != nil {
		panic(fmt.Errorf("load chainBasics faild, err: %v", res.Error))
	}
	//log.Info("q-w-e-r-t start to foreach tokenBasics")
	for _, basic := range tokenBasics {
		//log.Info(fmt.Sprintf("	for basicname: %v", basic.Name))
		assetDetail := new(AssetDetail)
		dstChainAssets := make([]DstChainAsset, 0)
		totalFlow := big.NewInt(0)
		for _, token := range basic.Tokens {
			chainAsset := new(DstChainAsset)
			chainAsset.ChainId = token.ChainId
			balance, err := common.GetBalance(token.ChainId, token.Hash)
			if err != nil {
				log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v", token.ChainId, token.Hash, err))
				balance = big.NewInt(0)
				//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetBalance faild, err: %v", token.ChainId, token.Hash, res.Error))
			}
			//log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, balance: %v", token.ChainId, token.Hash, balance.String()))
			chainAsset.Balance = balance
			//time sleep
			time.Sleep(time.Second)

			totalSupply, _ := common.GetTotalSupply(token.ChainId, token.Hash)
			if err != nil {
				totalSupply = big.NewInt(0)
				log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v ", token.ChainId, token.Hash, err))

				//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetTotalSupply faild, err: %v", token.ChainId, token.Hash, res.Error))
			}
			if !inExtraBasic(token.TokenBasicName) && basic.ChainId == token.ChainId {
				totalSupply = big.NewInt(0)
			}
			//specialBasic
			totalSupply = specialBasic(token, totalSupply)
			chainAsset.TotalSupply = totalSupply
			chainAsset.flow = new(big.Int).Sub(totalSupply, balance)
			//log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, flow: %v", token.ChainId, token.Hash, chainAsset.flow.String()))
			totalFlow = new(big.Int).Add(totalFlow, chainAsset.flow)
			dstChainAssets = append(dstChainAssets, *chainAsset)
		}
		assetDetail.Price = basic.Price
		assetDetail.Precision = basic.Precision
		assetDetail.TokenAsset = dstChainAssets
		log.Info(fmt.Sprintf("	basic: %v,totalFlow: %v", basic.Name, totalFlow.String()))
		assetDetail.Difference = totalFlow
		assetDetail.BasicName = basic.Name
		if inExtraBasic(assetDetail.BasicName) {
			extraAssetDetails = append(extraAssetDetails, assetDetail)
			continue
		}
		if assetDetail.BasicName == "WBTC" {
			chainAsset := new(DstChainAsset)
			chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
			response, err := http.Get("http://124.156.209.180:9999/balance/0x6c27318a0923369de04df7Edb818744641FD9602/0x7648bDF3B4f26623570bE4DD387Ed034F2E95aad")
			defer response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				log.Error("Get o3 WBTC err:", err)
				continue
			}
			body, _ := ioutil.ReadAll(response.Body)
			o3WBTC := struct {
				Balance *big.Int
			}{}
			json.Unmarshal(body, &o3WBTC)
			fmt.Println(o3WBTC.Balance)
			chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
			chainAsset.flow = o3WBTC.Balance
			assetDetail.TokenAsset = append(assetDetail.TokenAsset, *chainAsset)
			assetDetail.Difference.Add(assetDetail.Difference, chainAsset.flow)
		}
		if assetDetail.Difference.Cmp(big.NewInt(0)) == 1 {
			amount_usd := decimal.NewFromBigInt(assetDetail.Difference, 0).Div(decimal.NewFromInt(int64(assetDetail.Precision))).Mul(decimal.New(assetDetail.Price, -8))
			assetDetail.Amount_usd = amount_usd.BigInt()
			if amount_usd.Cmp(decimal.NewFromInt32(10000)) == 1 {
				assetDetail.Amount_usd1 = amount_usd.BigInt()
			}
		}

		resAssetDetails = append(resAssetDetails, assetDetail)
	}
	fmt.Println("---准确数据---")
	for _, assetDetail := range resAssetDetails {
		if assetDetail.Amount_usd1.Cmp(big.NewInt(0)) == 1 {
			title := "[poly_NB]"
			body := make(map[string]interface{})
			body[assetDetail.BasicName] = assetDetail
			err := common.PostDingCardSimple(title, body, []map[string]string{})
			if err != nil {
				fmt.Println("------------发送钉钉错误,错误---------")
			}
		}
		fmt.Println(assetDetail.BasicName, assetDetail.Difference, assetDetail.Precision, assetDetail.Price, assetDetail.Amount_usd, assetDetail.Amount_usd1)
		for _, tokenAsset := range assetDetail.TokenAsset {
			fmt.Printf("%2v %-30v %-30v %-30v\n", tokenAsset.ChainId, tokenAsset.TotalSupply, tokenAsset.Balance, tokenAsset.flow)
		}
	}
	fmt.Println("---BU准确数据---")
	for _, assetDetail := range extraAssetDetails {
		fmt.Println(assetDetail.BasicName, assetDetail.Difference)
		for _, tokenAsset := range assetDetail.TokenAsset {
			fmt.Printf("%2v %-30v %-30v %-30v\n", tokenAsset.ChainId, tokenAsset.TotalSupply, tokenAsset.Balance, tokenAsset.flow)
		}
	}
}
func inExtraBasic(name string) bool {
	extraBasics := []string{"BLES", "GOF", "LEV", "mBTM", "MOZ", "O3", "STN", "USDT", "XMPT"}
	for _, basic := range extraBasics {
		if name == basic {
			return true
		}
	}
	return false
}
func specialBasic(token *models.Token, totalSupply *big.Int) *big.Int {
	presion, _ := new(big.Int).SetString("1000000000000000000", 10)
	if token.TokenBasicName == "YNI" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "YNI" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("1000000000000000000", 10)
		return x
	}
	if token.TokenBasicName == "DAO" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("1000000000000000000000", 10)
		return x
	}
	if token.TokenBasicName == "DAO" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("1000000000000000000000", 10)
		return x
	}
	if token.TokenBasicName == "COPR" && token.ChainId == basedef.BSC_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("274400000", 10)
		return new(big.Int).Mul(x, presion)
	}
	if token.TokenBasicName == "COPR" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("0", 10)
		return x
	}
	if token.TokenBasicName == "DigiCol ERC-721" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "DigiCol ERC-721" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "DMOD" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "DMOD" && token.ChainId == basedef.BSC_CROSSCHAIN_ID {
		return new(big.Int).Mul(big.NewInt(15000000), presion)
	}
	if token.TokenBasicName == "SIL" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("1487520675265330391631", 10)
		return x
	}
	if token.TokenBasicName == "SIL" && token.ChainId == basedef.BSC_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("5001", 10)
		return x
	}
	return totalSupply
}

func test() {
	chainId := uint64(2)
	hash := "42d9fef0cbd9c3000cece9764d99a4a6fe9e1b34"
	balance, err := common.GetBalance(chainId, hash)
	if err != nil {
		log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v", chainId, hash, err))
		balance = big.NewInt(0)
		//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetBalance faild, err: %v", token.ChainId, token.Hash, res.Error))
	}
	totalSupply, _ := common.GetTotalSupply(chainId, hash)
	if err != nil {
		totalSupply = big.NewInt(0)
		log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v ", chainId, hash, err))

		//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetTotalSupply faild, err: %v", token.ChainId, token.Hash, res.Error))
	}
	//log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, balance: %v", token.ChainId, token.Hash, balance.String()))

	assetDetail1 := new(AssetDetail)
	assetDetail1.BasicName = "O3"
	chainAsset := new(DstChainAsset)
	chainAsset.Balance = balance
	chainAsset.TotalSupply = totalSupply
	assetDetail1.TokenAsset = append(assetDetail1.TokenAsset, *chainAsset)
	chainId = uint64(2)
	hash = "cb46c550539ac3db72dc7af7c89b11c306c727c2,"
	balance, err = common.GetBalance(chainId, hash)
	if err != nil {
		log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v", chainId, hash, err))
		balance = big.NewInt(0)
		//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetBalance faild, err: %v", token.ChainId, token.Hash, res.Error))
	}
	totalSupply, _ = common.GetTotalSupply(chainId, hash)
	if err != nil {
		totalSupply = big.NewInt(0)
		log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, err:%v ", chainId, hash, err))

		//panic(fmt.Errorf("q-w-e-r-t In CheckAsset Chain: %v,hash: %v , GetTotalSupply faild, err: %v", token.ChainId, token.Hash, res.Error))
	}
	//log.Info(fmt.Sprintf("	chainId: %v, Hash: %v, balance: %v", token.ChainId, token.Hash, balance.String()))

	assetDetail2 := new(AssetDetail)
	assetDetail2.BasicName = "ONG"
	chainAsset = new(DstChainAsset)
	chainAsset.Balance = balance
	chainAsset.TotalSupply = totalSupply
	assetDetail2.TokenAsset = append(assetDetail2.TokenAsset, *chainAsset)

	title := "[poly_NB]"
	body := make(map[string]interface{})
	body[assetDetail1.BasicName] = *assetDetail1
	body[assetDetail2.BasicName] = *assetDetail2
	err = common.PostDingCardSimple(title, body, []map[string]string{})
	if err != nil {
		fmt.Println("------------发送钉钉错误,错误---------")
	}
}
