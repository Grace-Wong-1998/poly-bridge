/*
 * Copyright (C) 2020 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package crosschainstats

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"poly-bridge/basedef"
	"poly-bridge/common"
	"poly-bridge/conf"
	"poly-bridge/crosschaindao/bridgedao"
	"poly-bridge/http/tools"
	"poly-bridge/models"
	"sync"
	"time"

	"github.com/beego/beego/v2/core/logs"
	"github.com/shopspring/decimal"
)

type Stats struct {
	context.Context
	cancel context.CancelFunc
	cfg    *conf.StatsConfig
	dao    *bridgedao.BridgeDao
	wg     sync.WaitGroup
	ipCfg  *conf.IPPortConfig
}

var ccs *Stats

// Start - Do stats aggregation/calculation
func StartCrossChainStats(server string, cfg *conf.StatsConfig, dbCfg *conf.DBConfig, ipCfg *conf.IPPortConfig) {
	if server != basedef.SERVER_POLY_BRIDGE {
		panic("CrossChainStats Only runs on bridge server")
	}
	if cfg == nil || cfg.TokenBasicStatsInterval == 0 || cfg.TokenAmountCheckInterval == 0 {
		panic("Invalid Stats config")
	}

	dao := bridgedao.NewBridgeDao(dbCfg, false)
	ctx, cancel := context.WithCancel(context.Background())
	ccs = &Stats{dao: dao, cfg: cfg, Context: ctx, cancel: cancel, ipCfg: ipCfg}
	ccs.Start()
}

// Stop
func StopCrossChainStats() {
	if ccs != nil {
		ccs.Stop()
	}
}

func (this *Stats) run(interval int64, f func() error) {
	this.wg.Add(1)
	ticker := time.NewTicker(time.Second * time.Duration(interval))
	for {
		select {
		case <-ticker.C:
			err := f()
			if err != nil {
				logs.Error("stats run error%s", err)
			}
		case <-this.Done():
			break
		}
	}
	this.wg.Done()
}

func (this *Stats) Start() {
	go this.run(this.cfg.TokenBasicStatsInterval, this.computeStats)
	go this.run(this.cfg.TokenAmountCheckInterval, this.computeTokensStats)
	go this.run(this.cfg.TokenStatisticInterval, this.computeTokenStatistics)
	go this.run(this.cfg.ChainStatisticInterval, this.computeChainStatistics)
	go this.run(this.cfg.ChainAddressCheckInterval, this.computeChainStatisticAssets)
	go this.run(this.cfg.AssetStatisticInterval, this.computeAssetStatistics)
	go this.run(this.cfg.AssetAdressInterval, this.computeAssetStatisticAdress)
	if basedef.ENV == basedef.MAINNET {
		go this.run(600, this.startCheckAssetAlarm)
	}
}

func (this *Stats) Stop() {
	logs.Info("Stopping stats server")
	this.cancel()
	this.wg.Wait()
}

func (this *Stats) computeStats() (err error) {
	logs.Info("Computing cross chain token basic stats")
	tokens, err := this.dao.GetTokenBasics()
	if err != nil {
		return fmt.Errorf("Failed to fetch token basic list %w", err)
	}
	for _, basic := range tokens {
		if len(basic.Tokens) > 0 {
			err := this.computeTokenBasicStats(basic)
			if err != nil {
				logs.Error("Failed to computeTokenBasicStats for %s err %v", basic.Name, err)
			}
		}
	}
	return
}

func (this *Stats) computeTokenBasicStats(token *models.TokenBasic) (err error) {
	logs.Info("Computing token basic stats for %s", token.Name)
	if len(token.Tokens) == 0 {
		return
	}
	assets := make([][]interface{}, len(token.Tokens))
	for i, t := range token.Tokens {
		assets[i] = []interface{}{t.ChainId, t.Hash}
	}
	checkPoint := token.StatsUpdateTime
	last, err := this.dao.GetLastSrcTransferForToken(assets)
	if err != nil || last == nil || checkPoint == int64(last.Time) {
		if err != nil || last == nil {
			logs.Error("Failed to get last src transfers for %s %+v err %v", *token, assets, err)
		}
		return err
	}
	totalAmount, totalCount, err := this.dao.AggregateTokenBasicSrcTransfers(assets, checkPoint, last.Id)
	if err != nil {
		return err
	}
	token.StatsUpdateTime = last.Id
	if checkPoint == 0 {
		token.TotalAmount = &models.BigInt{*totalAmount}
		token.TotalCount = totalCount
	} else {
		token.TotalAmount = &models.BigInt{*new(big.Int).Add(totalAmount, &token.TotalAmount.Int)}
		token.TotalCount += totalCount
	}
	v := new(big.Float).Quo(new(big.Float).SetInt(&token.TotalAmount.Int), new(big.Float).SetInt64(basedef.Int64FromFigure(int(token.Precision))))
	f, _ := v.Float32()
	tools.Record(f, "total_amount.%s", token.Name)
	tools.Record(token.TotalCount, "total_count.%s", token.Name)
	err = this.dao.UpdateTokenBasicStatsWithCheckPoint(token, checkPoint)
	return
}

func (this *Stats) computeTokensStats() (err error) {
	logs.Info("Computing cross chain token stats")
	tokens, err := this.dao.GetTokens()
	if err != nil {
		return fmt.Errorf("Failed to fetch token basic list %w", err)
	}
	for _, t := range tokens {
		amount, err := common.GetBalance(t.ChainId, t.Hash)
		if err != nil || amount == nil {
			logs.Error("Failed to fetch token available amount for token %s %v %s", t.Hash, t.ChainId, err)
			continue
		}
		v := new(big.Float).Quo(new(big.Float).SetInt(amount), new(big.Float).SetInt64(basedef.Int64FromFigure(int(t.Precision))))
		f, _ := v.Float32()
		tools.Record(f, "balance.%s.%v", t.TokenBasicName, t.ChainId)
		err = this.dao.UpdateTokenAvailableAmount(t.Hash, t.ChainId, amount)
		if err != nil {
			logs.Error("Failed to update token available amount for token %s %v %s", t.Hash, t.ChainId, err)
		}
	}
	return
}

func (this *Stats) computeTokenStatistics() (err error) {
	logs.Info("start computeTokenStatistics")
	newDst, err := this.dao.GetNewDstTransfer()
	if err != nil {
		return fmt.Errorf("Failed to GetNewDstTransfer %w", err)
	}
	nowInId := newDst.Id
	newSrc, err := this.dao.GetNewSrcTransfer()
	if err != nil {
		return fmt.Errorf("Failed to GetNewSrcTransfer %w", err)
	}
	nowOutId := newSrc.Id
	tokenStatistics, err := this.dao.GetTokenStatistics()
	if err != nil {
		return fmt.Errorf("Failed to GetTokenStatistics %w", err)
	}
	tokens, err := this.dao.GetTokens()
	if err != nil {
		return fmt.Errorf("Failed to GetTokens %w", err)
	}
	type chainhash struct {
		chainId uint64
		hash    string
	}
	tokensMap := make(map[chainhash]bool)
	for _, token := range tokens {
		if token.Standard == uint8(0) {
			a := chainhash{}
			a.chainId = token.ChainId
			a.hash = token.Hash
			tokensMap[a] = true
		}
	}
	for _, tokenSta := range tokenStatistics {
		a := chainhash{}
		a.chainId = tokenSta.ChainId
		a.hash = tokenSta.Hash
		tokensMap[a] = false
	}
	for k, v := range tokensMap {
		if v == true {
			tokenStatistic := new(models.TokenStatistic)
			tokenStatistic.ChainId = k.chainId
			tokenStatistic.Hash = k.hash
			tokenStatistic.InAmount = models.NewBigIntFromInt(0)
			tokenStatistic.InAmountUsd = models.NewBigIntFromInt(0)
			tokenStatistic.InAmountBtc = models.NewBigIntFromInt(0)
			tokenStatistic.OutAmount = models.NewBigIntFromInt(0)
			tokenStatistic.OutAmountUsd = models.NewBigIntFromInt(0)
			tokenStatistic.OutAmountBtc = models.NewBigIntFromInt(0)
			tokenStatistic.InCounter = 0
			tokenStatistic.OutCounter = 0
			tokenStatistic.LastInCheckId = 0
			tokenStatistic.LastOutCheckId = 0
			tokenStatistics = append(tokenStatistics, tokenStatistic)
		}
	}
	tokenBasicBTC, err := this.dao.GetBTCPrice()
	if err != nil {
		return fmt.Errorf("Failed to GetBTCPrice %w", err)
	}
	BTCPrice := decimal.NewFromInt(tokenBasicBTC.Price).Div(decimal.NewFromInt(basedef.PRICE_PRECISION))
	logs.Info("BTCPrice:", BTCPrice)
	for _, statistic := range tokenStatistics {
		token, err := this.dao.GetTokenBasicByHash(statistic.ChainId, statistic.Hash)
		if err == nil {
			logs.Error("this_dao_GetTokenBasicByHash err", err)
			continue
		}
		price_new := decimal.New(token.TokenBasic.Price, 0).Div(decimal.NewFromInt(basedef.PRICE_PRECISION))
		precision_new := decimal.New(int64(1), int32(token.Precision))
		logs.Info("qwertprecision_new in", precision_new)
		if token.TokenBasic.ChainId == statistic.ChainId {
			balance, err := getAndRetryBalance(statistic.ChainId, statistic.Hash)
			if err != nil {
				logs.Info("CheckAsset chainId: %v, Hash: %v, err:%v", statistic.ChainId, statistic.Hash, err)
			} else {
				amount_new := decimal.NewFromBigInt(balance, 0)
				statistic.InAmount = models.NewBigInt(amount_new.Div(precision_new).Mul(decimal.NewFromInt32(100)).BigInt())
			}
		} else {
			in, err := this.dao.CalculateInTokenStatistics(statistic.ChainId, statistic.Hash, statistic.LastInCheckId, nowInId)
			if err != nil {
				logs.Error("Failed to CalculateInTokenStatistics %w", err)
			}
			if in != nil && in.Token != nil && in.Token.TokenBasic != nil {
				amount_new := decimal.NewFromBigInt(&in.InAmount.Int, 0)
				statistic.InAmount = addDecimalBigInt(statistic.InAmount, models.NewBigInt(amount_new.Div(precision_new).Mul(decimal.NewFromInt32(100)).BigInt()))
				statistic.InCounter = addDecimalInt64(statistic.InCounter, in.InCounter)
			}
		}
		amount_usd := decimal.NewFromBigInt(&statistic.InAmount.Int, 0).Mul(price_new)
		amount_btc := amount_usd.Div(BTCPrice)
		statistic.InAmountUsd = models.NewBigInt(amount_usd.Mul(decimal.NewFromInt32(100)).BigInt())
		statistic.InAmountBtc = models.NewBigInt(amount_btc.Mul(decimal.NewFromInt32(100)).BigInt())

		out, err := this.dao.CalculateOutTokenStatistics(statistic.ChainId, statistic.Hash, statistic.LastInCheckId, nowInId)
		if err != nil {
			logs.Error("Failed to CalculateOutTokenStatistics %w", err)
		}
		if out != nil && out.Token != nil && out.Token.TokenBasic != nil {
			if statistic.ChainId == out.Token.TokenBasic.ChainId {
				statistic.OutAmount = models.NewBigIntFromInt(0)
			} else {
				amount_new := decimal.NewFromBigInt(&out.OutAmount.Int, 0)
				statistic.OutAmount = addDecimalBigInt(statistic.OutAmount, models.NewBigInt(amount_new.Div(precision_new).Mul(decimal.NewFromInt32(100)).BigInt()))
			}
			amount_usd := decimal.NewFromBigInt(&statistic.OutAmount.Int, 0).Mul(price_new)
			amount_btc := amount_usd.Div(BTCPrice)

			statistic.OutCounter = addDecimalInt64(statistic.OutCounter, out.OutCounter)
			statistic.OutAmountUsd = models.NewBigInt(amount_usd.Mul(decimal.NewFromInt32(100)).BigInt())
			statistic.OutAmountBtc = models.NewBigInt(amount_btc.Mul(decimal.NewFromInt32(100)).BigInt())
		}
		statistic.LastInCheckId = nowInId
		statistic.LastOutCheckId = nowOutId
		err = this.dao.SaveTokenStatistic(statistic)
		if err != nil {
			return fmt.Errorf("Failed to SaveTokenStatistic %w", err)
		}
	}
	return nil
}

func (this *Stats) computeChainStatistics() (err error) {
	logs.Info("computeChainStatistics,start_computeChainStatistics_computeChainStatistics")
	nowChainStatistic, err := this.dao.GetNewChainSta()
	if err != nil {
		return fmt.Errorf("Failed to GetNewChainSta %w", err)
	}
	nowIn, err := this.dao.GetNewDstTransaction()
	if err != nil {
		return fmt.Errorf("Failed to GetNewDstTransfer %w", err)
	}
	nowInId := nowIn.Id
	nowOut, err := this.dao.GetNewSrcTransaction()
	if err != nil {
		return fmt.Errorf("Failed to GetNewSrcTransfer %w", err)
	}
	nowOutId := nowOut.Id
	inChainStatistics := make([]*models.ChainStatistic, 0)
	if nowInId > nowChainStatistic.LastInCheckId {
		err = this.dao.CalculateInChainStatistics(nowChainStatistic.LastInCheckId, nowInId, &inChainStatistics)
		if err != nil {
			logs.Error("Failed to CalculateInTokenStatistics %w", err)
		}
	}
	outChainStatistics := make([]*models.ChainStatistic, 0)
	if nowOutId > nowChainStatistic.LastOutCheckId {
		err = this.dao.CalculateOutChainStatistics(nowChainStatistic.LastOutCheckId, nowOutId, &outChainStatistics)
		if err != nil {
			logs.Error("Failed to CalculateInTokenStatistics %w", err)
		}
	}
	polyTransaction, err := this.dao.GetPolyTransaction()
	if err != nil {
		return fmt.Errorf("Failed to GetPolyTransaction %w", err)
	}
	polyCheckId := polyTransaction.Id
	if nowInId > nowChainStatistic.LastInCheckId || nowOutId > nowChainStatistic.LastOutCheckId {
		logs.Info("computeChainStatistics,nowInId > nowChainStatistic.LastInCheckId || nowOutId > nowChainStatistic.LastOutCheckId")
		chainStatistics := make([]*models.ChainStatistic, 0)
		err = this.dao.GetChainStatistic(&chainStatistics)
		if err != nil {
			return fmt.Errorf("Failed to GetChainStatistic %w", err)
		}
		chainMap := make(map[uint64]bool)
		chains, err := this.dao.GetChains()
		if err != nil {
			return fmt.Errorf("Failed to GetChains %w", err)
		}
		for _, v := range chains {
			chainMap[v.ChainId] = false
		}
		for _, v := range chainStatistics {
			chainMap[v.ChainId] = true
		}
		for k, v := range chainMap {
			if v == false {
				chainStatistic := new(models.ChainStatistic)
				chainStatistic.ChainId = k
				chainStatistic.In = 0
				chainStatistic.Out = 0
				chainStatistic.Addresses = 0
				chainStatistic.LastInCheckId = nowInId
				chainStatistic.LastOutCheckId = nowOutId
				chainStatistics = append(chainStatistics, chainStatistic)
			}
		}
		for _, chainStatistic := range chainStatistics {
			for _, in := range inChainStatistics {
				if chainStatistic.ChainId == in.ChainId && chainStatistic.ChainId != basedef.POLY_CROSSCHAIN_ID {
					chainStatistic.In = addDecimalInt64(chainStatistic.In, in.In)
					break
				}
			}
			for _, out := range outChainStatistics {
				if chainStatistic.ChainId == out.ChainId && chainStatistic.ChainId != basedef.POLY_CROSSCHAIN_ID {
					chainStatistic.Out = addDecimalInt64(chainStatistic.Out, out.Out)
					break
				}
			}
			if chainStatistic.ChainId != basedef.POLY_CROSSCHAIN_ID {
				chainStatistic.LastInCheckId = nowInId
				chainStatistic.LastOutCheckId = nowOutId
			}
		}
		logs.Info("computeChainStatistics,poly_polyCheckId", polyCheckId)
		for _, chainStatistic := range chainStatistics {
			if chainStatistic.ChainId == basedef.POLY_CROSSCHAIN_ID {
				counter, err := this.dao.CalculatePolyChainStatistic(chainStatistic.LastInCheckId, polyCheckId)
				if err == nil {
					logs.Info("computeChainStatistics,polychainid:", chainStatistic.ChainId, "poly.In:", chainStatistic.In, "poly.Out:", chainStatistic.Out, "poly.LastInCheckId1", chainStatistic.LastInCheckId, "polycounter:", counter)
					chainStatistic.In = addDecimalInt64(counter, chainStatistic.In)
					chainStatistic.Out = addDecimalInt64(counter, chainStatistic.Out)
					chainStatistic.LastInCheckId = polyCheckId
					chainStatistic.LastOutCheckId = polyCheckId
				}
				break
			}
		}
		err = this.dao.SaveChainStatistics(chainStatistics)
		if err != nil {
			logs.Error("qChainStatistic,computeChainStatisticAssets SaveChainStatistic error", err)
		}
	}
	return
}
func (this *Stats) computeChainStatisticAssets() (err error) {
	logs.Info("computeChainStatisticAssets,start computeChainStatisticAssets")
	computeChainStatistics := make([]*models.ChainStatistic, 0)
	err = this.dao.CalculateChainStatisticAssets(&computeChainStatistics)
	if err != nil {
		return fmt.Errorf("Failed to CalculateChainStatisticAssets %w", err)
	}
	chainStatistics := make([]*models.ChainStatistic, 0)
	err = this.dao.GetChainStatistic(&chainStatistics)
	if err != nil {
		logs.Error("computeChainStatisticAssets GetChainStatistic error", err)
	}
	polyAddress := int64(0)
	for _, chainStatistic := range chainStatistics {
		for _, chain := range computeChainStatistics {
			if chainStatistic.ChainId == chain.ChainId && chainStatistic.ChainId != basedef.POLY_CROSSCHAIN_ID {
				chainStatistic.Addresses = chain.Addresses
				polyAddress += chain.Addresses
				break
			}
		}
	}
	for _, chainStatistic := range chainStatistics {
		if chainStatistic.ChainId == basedef.POLY_CROSSCHAIN_ID {
			chainStatistic.Addresses = polyAddress
		}
	}
	err = this.dao.SaveChainStatistics(chainStatistics)
	if err != nil {
		logs.Error("computeChainStatisticAssets SaveChainStatistics error", err)
	}
	logs.Info("computeChainStatisticAssets,end computeChainStatisticAssets")

	return
}

func (this *Stats) computeAssetStatistics() (err error) {
	logs.Info("computeAssetStatistics,start computeAssetStatistics")
	srcTransfer, err := this.dao.GetNewSrcTransfer()
	if err != nil {
		return fmt.Errorf("Failed to GetNewSrcTransfer %w", err)
	}
	nowId := srcTransfer.Id
	nameMap := make(map[string]bool)
	tokenBasics, err := this.dao.GetTokenBasics()
	if err != nil {
		return fmt.Errorf("Failed to GetTokenBasics %w", err)
	}
	for _, v := range tokenBasics {
		if v.Standard == uint8(0) {
			nameMap[v.Name] = false
		}
	}
	assetStatistics, err := this.dao.GetAssetStatistic()
	if err != nil {
		return fmt.Errorf("Failed to GetAssetStatistic %w", err)
	}
	for _, v := range assetStatistics {
		nameMap[v.TokenBasicName] = true
	}
	for k, v := range nameMap {
		if v == false {
			assetStatistic := new(models.AssetStatistic)
			assetStatistic.TokenBasicName = k
			assetStatistic.Addressnum = 0
			assetStatistic.Txnum = 0
			assetStatistic.Amount = models.NewBigIntFromInt(0)
			assetStatistic.AmountUsd = models.NewBigIntFromInt(0)
			assetStatistic.AmountBtc = models.NewBigIntFromInt(0)
			assetStatistic.LastCheckId = 0
			assetStatistics = append(assetStatistics, assetStatistic)
		}
	}
	tokenBasicBTC, err := this.dao.GetBTCPrice()
	if err != nil {
		return fmt.Errorf("Failed to GetBTCPrice %w", err)
	}
	BTCPrice := decimal.NewFromInt(tokenBasicBTC.Price).Div(decimal.NewFromInt(basedef.PRICE_PRECISION))
	for _, old := range assetStatistics {
		assetInfos, err := this.dao.CalculateAssets(old.TokenBasicName, old.LastCheckId, nowId)
		if err != nil {
			logs.Error("Failed to CalculateAssets %w", err)
		}
		for _, assetInfo := range assetInfos {
			amount_new := decimal.NewFromBigInt(&assetInfo.Amount.Int, 0)
			precision_new := decimal.New(int64(1), int32(assetInfo.Precision))
			real_amount := amount_new.Div(precision_new)
			price_new := decimal.NewFromInt(assetInfo.Price).Div(decimal.NewFromInt(basedef.PRICE_PRECISION))
			amount_usd := real_amount.Mul(price_new)
			amount_btc := amount_usd.Div(BTCPrice)

			old.Amount = models.NewBigInt((real_amount.Mul(decimal.New(int64(100), 0)).Add(decimal.NewFromBigInt(&old.Amount.Int, 0))).BigInt())
			old.AmountUsd = models.NewBigInt((amount_usd.Mul(decimal.New(int64(10000), 0)).Add(decimal.NewFromBigInt(&old.AmountUsd.Int, 0))).BigInt())
			old.AmountBtc = models.NewBigInt((amount_btc.Mul(decimal.New(int64(10000), 0)).Add(decimal.NewFromBigInt(&old.AmountBtc.Int, 0))).BigInt())

			old.Txnum = old.Txnum + assetInfo.Txnum
		}
		old.LastCheckId = nowId
		err = this.dao.SaveAssetStatistic(old)
		if err != nil {
			return fmt.Errorf("Failed to UpdateTransferStatistic %w", err)
		}
	}
	logs.Info("computeAssetStatistics,end computeAssetStatistics")

	return nil
}

func (this *Stats) computeAssetStatisticAdress() (err error) {
	logs.Info("start computeAssetStatisticAdress")
	newAssetAdresses, err := this.dao.CalculateAssetAdress()
	if err != nil {
		return fmt.Errorf("Failed to CalculateAssetAdress %w", err)
	}
	for _, assetStatistic := range newAssetAdresses {
		err := this.dao.UpdateAssetStatisticAdress(assetStatistic)
		if err != nil {
			return fmt.Errorf("Failed to UpdateAssetStatisticAdress %w", err)
		}
	}
	return nil
}

func addDecimalBigInt(a, b *models.BigInt) *models.BigInt {
	a_new := decimal.NewFromBigInt(&a.Int, 0)
	b_new := decimal.NewFromBigInt(&b.Int, 0)
	c := a_new.Add(b_new)
	return models.NewBigInt(c.BigInt())
}
func addDecimalInt64(a, b int64) int64 {
	a_new := decimal.New(a, 0)
	b_new := decimal.New(b, 0)
	c := a_new.Add(b_new)
	return c.IntPart()
}

type DstChainAsset struct {
	ChainId     uint64
	Hash        string
	TotalSupply *big.Int
	Balance     *big.Int
	Flow        *big.Int
}
type AssetDetail struct {
	BasicName  string
	TokenAsset []*DstChainAsset
	Difference *big.Int
	Precision  uint64
	Price      int64
	Amount_usd string
	Reason     string
}

func (this *Stats) startCheckAssetAlarm() (err error) {
	logs.Info("StartCheckAsset,start startCheckAsset")
	if err != nil {
		return err
	}
	resAssetDetails := make([]*AssetDetail, 0)
	extraAssetDetails := make([]*AssetDetail, 0)

	tokenBasics, err := this.dao.GetPropertytokenBasic()
	if err != nil {
		return err
	}
	for _, basic := range tokenBasics {
		assetDetail := new(AssetDetail)
		dstChainAssets := make([]*DstChainAsset, 0)
		totalFlow := big.NewInt(0)
		for _, token := range basic.Tokens {
			if notToken(token) {
				continue
			}
			if token.Property != int64(1) {
				continue
			}
			chainAsset := new(DstChainAsset)
			chainAsset.ChainId = token.ChainId
			chainAsset.Hash = token.Hash
			balance, err := getAndRetryBalance(token.ChainId, token.Hash)
			if err != nil {
				assetDetail.Reason = err.Error()
				logs.Info("CheckAsset chainId: %v, Hash: %v, err:%v", token.ChainId, token.Hash, err)
				balance = big.NewInt(0)
			}
			chainAsset.Balance = balance
			time.Sleep(time.Second)
			totalSupply, err := getAndRetryTotalSupply(token.ChainId, token.Hash)
			if err != nil {
				assetDetail.Reason = err.Error()
				totalSupply = big.NewInt(0)
				logs.Info("CheckAsset chainId: %v, Hash: %v, err:%v ", token.ChainId, token.Hash, err)
			}
			//specialBasic
			totalSupply = specialBasic(token, totalSupply)
			//original asset
			if !inExtraBasic(token.TokenBasicName) && basic.ChainId == token.ChainId {
				totalSupply = big.NewInt(0)
			}
			chainAsset.TotalSupply = totalSupply
			chainAsset.Flow = new(big.Int).Sub(totalSupply, balance)
			totalFlow = new(big.Int).Add(totalFlow, chainAsset.Flow)
			dstChainAssets = append(dstChainAssets, chainAsset)
		}
		assetDetail.Price = basic.Price
		assetDetail.Precision = basic.Precision
		assetDetail.TokenAsset = dstChainAssets
		assetDetail.Difference = totalFlow
		assetDetail.BasicName = basic.Name
		//03 (WBTC,USDT)
		getO3Data(assetDetail, this.ipCfg)
		if inExtraBasic(assetDetail.BasicName) {
			extraAssetDetails = append(extraAssetDetails, assetDetail)
			continue
		}
		if assetDetail.Difference.Cmp(big.NewInt(0)) == 1 {
			assetDetail.Amount_usd = decimal.NewFromBigInt(assetDetail.Difference, 0).Div(decimal.New(1, int32(assetDetail.Precision))).Mul(decimal.New(assetDetail.Price, -8)).StringFixed(0)
		}

		resAssetDetails = append(resAssetDetails, assetDetail)
	}
	err = sendDing(resAssetDetails, this.ipCfg.DingIP)
	if err != nil {
		logs.Error("------------sendDingDINg err---------")
	}
	logs.Info("CheckAsset rightdata___")
	for _, assetDetail := range resAssetDetails {
		logs.Info("CheckAsset:", assetDetail.BasicName, assetDetail.Difference, assetDetail.Precision, assetDetail.Price, assetDetail.Amount_usd)
		for _, tokenAsset := range assetDetail.TokenAsset {
			logs.Info("CheckAsset %2v %-30v %-30v %-30v %-30v\n", tokenAsset.ChainId, tokenAsset.Hash, tokenAsset.TotalSupply, tokenAsset.Balance, tokenAsset.Flow)
		}
	}
	logs.Info("CheckAsset wrongdata___")
	for _, assetDetail := range extraAssetDetails {
		logs.Info("CheckAsset:", assetDetail.BasicName, assetDetail.Difference, assetDetail.Precision, assetDetail.Price)
		for _, tokenAsset := range assetDetail.TokenAsset {
			logs.Info("CheckAsset %2v %-30v %-30v %-30v %-30v\n", tokenAsset.ChainId, tokenAsset.Hash, tokenAsset.TotalSupply, tokenAsset.Balance, tokenAsset.Flow)
		}
	}
	return nil
}
func inExtraBasic(name string) bool {
	extraBasics := []string{"BLES", "GOF", "LEV", "mBTM", "MOZ", "O3", "USDT", "STN", "XMPT"}
	for _, basic := range extraBasics {
		if name == basic {
			return true
		}
	}
	return false
}
func specialBasic(token *models.Token, totalSupply *big.Int) *big.Int {
	presion := decimal.New(1, int32(token.Precision)).BigInt()
	if token.TokenBasicName == "YNI" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "YNI" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		return new(big.Int).Mul(big.NewInt(1), presion)
	}
	if token.TokenBasicName == "DAO" && token.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
		return new(big.Int).Mul(big.NewInt(1000), presion)
	}
	if token.TokenBasicName == "DAO" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		return new(big.Int).Mul(big.NewInt(1000), presion)
	}
	if token.TokenBasicName == "COPR" && token.ChainId == basedef.BSC_CROSSCHAIN_ID {
		return new(big.Int).Mul(big.NewInt(274400000), presion)
	}
	if token.TokenBasicName == "COPR" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		return big.NewInt(0)
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
		return new(big.Int).Mul(big.NewInt(5001), presion)
	}
	if token.TokenBasicName == "DOGK" && token.ChainId == basedef.BSC_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "DOGK" && token.ChainId == basedef.HECO_CROSSCHAIN_ID {
		x, _ := new(big.Int).SetString("285000000000", 10)
		return new(big.Int).Mul(x, presion)
	}
	if token.TokenBasicName == "SXC" && token.ChainId == basedef.OK_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "SXC" && token.ChainId == basedef.MATIC_CROSSCHAIN_ID {
		return big.NewInt(0)
	}
	if token.TokenBasicName == "OOE" && token.ChainId == basedef.MATIC_CROSSCHAIN_ID {
		return big.NewInt(0)
	}

	return totalSupply
}
func notToken(token *models.Token) bool {
	if token.TokenBasicName == "USDT" && token.Precision != 6 {
		return true
	}
	return false
}
func getO3Data(assetDetail *AssetDetail, ipCfg *conf.IPPortConfig) {
	switch assetDetail.BasicName {
	case "WBTC":
		chainAsset := new(DstChainAsset)
		chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
		response, err := http.Get(ipCfg.WBTCIP)
		defer response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			logs.Error("Get o3 WBTC err:", err)
			return
		}
		body, _ := ioutil.ReadAll(response.Body)
		o3WBTC := struct {
			Balance *big.Int
		}{}
		json.Unmarshal(body, &o3WBTC)
		chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
		chainAsset.TotalSupply = big.NewInt(0)
		chainAsset.Balance = big.NewInt(0)
		chainAsset.Flow = o3WBTC.Balance
		assetDetail.TokenAsset = append(assetDetail.TokenAsset, chainAsset)
		assetDetail.Difference.Add(assetDetail.Difference, chainAsset.Flow)
	case "USDT":
		chainAsset := new(DstChainAsset)
		chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
		response, err := http.Get(ipCfg.USDTIP)
		defer response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			logs.Error("Get o3 USDT err:", err)
			return
		}
		body, _ := ioutil.ReadAll(response.Body)
		o3USDT := struct {
			Balance *big.Int
		}{}
		json.Unmarshal(body, &o3USDT)
		chainAsset.ChainId = basedef.O3_CROSSCHAIN_ID
		chainAsset.Balance = o3USDT.Balance
		chainAsset.TotalSupply = big.NewInt(0)
		chainAsset.Flow = big.NewInt(0)
		assetDetail.TokenAsset = append(assetDetail.TokenAsset, chainAsset)
	}
}

func getAndRetryBalance(chainId uint64, hash string) (*big.Int, error) {
	balance, err := common.GetBalance(chainId, hash)
	if err != nil {
		for i := 0; i < 4; i++ {
			time.Sleep(time.Second)
			balance, err = common.GetBalance(chainId, hash)
			if err == nil {
				break
			}
		}
	}
	return balance, err
}

func getAndRetryTotalSupply(chainId uint64, hash string) (*big.Int, error) {
	totalSupply, err := common.GetTotalSupply(chainId, hash)
	if err != nil {
		for i := 0; i < 2; i++ {
			time.Sleep(time.Second)
			totalSupply, err = common.GetTotalSupply(chainId, hash)
			if err == nil {
				break
			}
		}
	}
	return totalSupply, err
}

func sendDing(assetDetails []*AssetDetail, dingUrl string) error {
	ss := "[poly_NB]_[mainnet]\n"
	flag := false
	for _, assetDetail := range assetDetails {
		if assetDetail.Reason == "all node is not working" {
			continue
		}
		if assetDetail.Difference.Cmp(big.NewInt(0)) == 1 {
			usd, _ := decimal.NewFromString(assetDetail.Amount_usd)
			if usd.Cmp(decimal.NewFromInt32(10000)) == 1 {
				flag = true
				ss += fmt.Sprintf("【%v】totalflow:%v $%v\n", assetDetail.BasicName, decimal.NewFromBigInt(assetDetail.Difference, 0).Div(decimal.New(1, int32(assetDetail.Precision))).StringFixed(2), assetDetail.Amount_usd)
				for _, x := range assetDetail.TokenAsset {
					ss += "ChainId: " + fmt.Sprintf("%v", x.ChainId) + "\n"
					ss += "Hash: " + fmt.Sprintf("%v", x.Hash) + "\n"
					logs.Info("x.TotalSupply:", x.TotalSupply)
					ss += "TotalSupply: " + decimal.NewFromBigInt(x.TotalSupply, 0).Div(decimal.New(1, int32(assetDetail.Precision))).StringFixed(2) + " "
					ss += "Balance: " + decimal.NewFromBigInt(x.Balance, 0).Div(decimal.New(1, int32(assetDetail.Precision))).StringFixed(2) + " "
					ss += "Flow: " + decimal.NewFromBigInt(x.Flow, 0).Div(decimal.New(1, int32(assetDetail.Precision))).StringFixed(2) + "\n"
				}
			}
		}
	}
	if flag {
		err := common.PostDingtext(ss, dingUrl)
		return err
	}
	return nil
}
