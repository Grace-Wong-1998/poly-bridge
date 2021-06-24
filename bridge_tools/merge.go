package main

import (
	"encoding/json"
	"fmt"
	"os"
	"poly-bridge/basedef"
	"poly-bridge/conf"
	"poly-bridge/crosschaindao/explorerdao"
	"poly-bridge/models"
	"reflect"
	"strings"
	"time"

	"github.com/astaxie/beego/logs"
	"github.com/cosmos/cosmos-sdk/types"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type MergeConfig struct {
	Bridge   *conf.DBConfig
	Explorer *conf.DBConfig
	DB       *conf.DBConfig
}

/* Steps
 * - createTables
 * - migrateBridgeBasicTables
 * - migrateExplorerSrcTransactions
 * - migrateExplorerPolyTransactions
 * - migrateExplorerDstTransactions
 * - migrateBridgeTxs
 * - migrateExplorerBasicTables
 * - verifyTables
 */

func checkError(err error, msg string) {
	if err != nil {
		panic(fmt.Sprintf("Fatal: %s error %+v", msg, err))
	}
}

func AddressAsHash(chainId uint64, value string) string {
	if chainId == basedef.NEO_CROSSCHAIN_ID && strings.HasPrefix(value, "swth") {
		chainId = basedef.COSMOS_CROSSCHAIN_ID
	}
	hash, _ := basedef.Address2Hash(chainId, value)
	return hash
}

func merge() {

	{
		config := types.GetConfig()
		config.SetBech32PrefixForAccount("swth", "swthpub")
		config.SetBech32PrefixForValidator("swthvaloper", "swthvaloperpub")
		config.SetBech32PrefixForConsensusNode("swthvalcons", "swthvalconspub")
	}

	configFile := os.Getenv("MERGE_CONFIG")
	if configFile == "" {
		configFile = "./merge.json"
	}

	fileContent, err := basedef.ReadFile(configFile)
	if err != nil {
		logs.Error("NewServiceConfig: failed, err: %s", err)
		return
	}
	config := &MergeConfig{}
	err = json.Unmarshal(fileContent, config)
	if err != nil {
		logs.Error("NewServiceConfig: failed, err: %s", err)
		return
	}
	if config.Bridge == nil || config.Explorer == nil || config.DB == nil {
		logs.Error("Invalid merge config, missing db conn %s", string(fileContent))
		return
	}

	step := os.Getenv("MERGE_STEP")
	if step == "" {
		panic("Invalid step")
	}

	logs.Info("Executing merge step %s", step)
	logs.Info("Bridge db config: %+v", *config.Bridge)
	logs.Info("Explorer db config: %+v", *config.Explorer)
	logs.Info("DB config: %+v", *config.DB)

	conn := func(cfg *conf.DBConfig) *gorm.DB {
		Logger := logger.Default
		Logger = Logger.LogMode(logger.Info)
		db, err := gorm.Open(mysql.Open(cfg.User+":"+cfg.Password+"@tcp("+cfg.URL+")/"+
			cfg.Scheme+"?charset=utf8"), &gorm.Config{Logger: Logger})
		checkError(err, "Connecting to db")
		return db
	}

	bri := conn(config.Bridge)
	exp := conn(config.Explorer)
	db := conn(config.DB)
	switch step {
	case "createTables":
		createTables(db)
	case "migrateBridgeBasicTables":
		migrateBridgeBasicTables(bri, db)
	case "migrateExplorerSrcTransactions":
		migrateExplorerSrcTransactions(exp, db)
	case "migrateExplorerPolyTransactions":
		migrateExplorerPolyTransactions(exp, db)
	case "migrateExplorerDstTransactions":
		migrateExplorerDstTransactions(exp, db)
	case "migrateBridgeTxs":
		migrateBridgeTxs(bri, db)
	case "migrateExplorerBasicTables":
		migrateExplorerBasicTables(exp, db)
	case "verifyTables":
		verifyTables(bri, db)
	default:
		logs.Error("Invalid step %s", step)
	}
}

func migrateTable(src, dst *gorm.DB, table string, model interface{}) {
	logs.Info("Migrating table %s", table)
	err := src.Find(model).Error
	checkError(err, "Loading table")
	err = dst.Save(model).Error
	checkError(err, "Saving table")
	countTables(table, table, src, dst)
}

func migrateBridgeBasicTables(bri, db *gorm.DB) {
	migrateTable(bri, db, "token_basics", &[]*models.TokenBasic{})
	migrateTable(bri, db, "price_markets", &[]*models.PriceMarket{})
	migrateTable(bri, db, "chains", &[]*models.Chain{})
	migrateTable(bri, db, "chain_fees", &[]*models.ChainFee{})
	migrateTable(bri, db, "nft_profiles", &[]*models.NFTProfile{})
	migrateTable(bri, db, "tokens", &[]*models.Token{})
	migrateTable(bri, db, "token_maps", &[]*models.TokenMap{})
}

func migrateExplorerBasicTables(exp, db *gorm.DB) {
	{
		logs.Info("Migrating table chains from explorer")
		model := make([]*explorerdao.Chain, 0)
		err := exp.Find(&model).Error
		checkError(err, "Loading table")
		for _, chain := range model {
			err = db.Table("chains").Where("chain_id=?", chain.ChainId).Update("name", chain.Name).Error
			checkError(err, "Saving table")
		}
	}
	{
		logs.Info("Migrating table tokens from explorer")
		model := make([]*explorerdao.Token, 0)
		err := exp.Find(&model).Error
		checkError(err, "Loading table")
		for _, token := range model {
			err = db.Table("tokens").Where("chain_id=? AND hash=?", token.Id, token.Hash).Update("token_type", token.Type).Error
			checkError(err, "Saving table")
		}
	}
}

func createTables(db *gorm.DB) {
	err := db.Debug().AutoMigrate(
		&models.DstTransaction{},
		&models.SrcTransaction{},
		&models.WrapperTransaction{},
		&models.TokenBasic{},
		&models.PriceMarket{},
		&models.Chain{},
		&models.ChainFee{},
		&models.DstSwap{},
		&models.DstTransfer{},
		&models.NFTProfile{},
		&models.PolyTransaction{},
		&models.SrcSwap{},
		&models.SrcTransfer{},
		&models.TimeStatistic{},
		&models.Token{},
		&models.TokenMap{},
	)
	checkError(err, "Creating tables")
}

func countTables(tableA, tableB string, src, dst *gorm.DB) {
	var a, b int64
	err := src.Table(tableA).Count(&a).Error
	checkError(err, "count table error")
	err = src.Table(tableA).Count(&b).Error
	checkError(err, "count table error")
	logs.Info("===============  Compare table size %s %d:%d %s ============", tableA, a, b, tableB)
}

func migrateExplorerSrcTransactions(exp, db *gorm.DB) {
	logs.Info("Runnign migrateExplorerSrcTransactions")
	selectNum := 1000
	count := 0
	for {
		logs.Info("migrateExplorerSrcTransactions %d", count)
		srcTransactions := make([]*explorerdao.SrcTransaction, 0)
		//exp.Preload("SrcTransfer").Order("tt asc").Limit(selectNum).Find(&srcTransactions)
		err := exp.Preload("SrcTransfer").Limit(selectNum).Offset(selectNum * count).Order("tt asc").Find(&srcTransactions).Error
		if err != nil {
			panic(err)
		}
		if len(srcTransactions) > 0 {
			srcTransactionsJson, err := json.Marshal(srcTransactions)
			if err != nil {
				panic(err)
			}
			newSrcTransactions := make([]*models.SrcTransaction, 0)
			err = json.Unmarshal(srcTransactionsJson, &newSrcTransactions)
			if err != nil {
				panic(err)
			}
			for _, transaction := range newSrcTransactions {
				transaction.User = AddressAsHash(transaction.ChainId, transaction.User)
				if transaction.SrcTransfer != nil {
					if transaction.SrcTransfer.ChainId != basedef.COSMOS_CROSSCHAIN_ID {
						transaction.SrcTransfer.From = AddressAsHash(transaction.SrcTransfer.ChainId, transaction.SrcTransfer.From)
					}
					transaction.SrcTransfer.To = AddressAsHash(transaction.SrcTransfer.ChainId, transaction.SrcTransfer.To)
					transaction.SrcTransfer.DstUser = AddressAsHash(transaction.SrcTransfer.DstChainId, transaction.SrcTransfer.DstUser)
				}
				if transaction.ChainId == basedef.ETHEREUM_CROSSCHAIN_ID {
					transaction.Hash, transaction.Key = transaction.Key, transaction.Hash
				}
			}
			err = db.Save(newSrcTransactions).Error
			if err != nil {
				panic(err)
			}
			count++
			time.Sleep(time.Second * 1)
		} else {
			break
		}
	}
	countTables("fchain_tx", "src_transactions", exp, db)
}

func migrateExplorerPolyTransactions(exp, db *gorm.DB) {
	logs.Info("Runnign migrateExplorerPolyTransactions")
	selectNum := 1000
	count := 0
	for {
		logs.Info("migrateExplorerPolyTransactions %d", count)
		polyTransactions := make([]*explorerdao.PolyTransaction, 0)
		//exp.Order("tt asc").Limit(selectNum).Find(&polyTransactions)
		err := exp.Order("tt asc").Limit(selectNum).Offset(selectNum * count).Order("tt asc").Find(&polyTransactions).Error
		if err != nil {
			panic(err)
		}
		if len(polyTransactions) > 0 {
			polyTransactionsJson, err := json.Marshal(polyTransactions)
			if err != nil {
				panic(err)
			}
			newPolyTransactions := make([]*models.PolyTransaction, 0)
			err = json.Unmarshal(polyTransactionsJson, &newPolyTransactions)
			if err != nil {
				panic(err)
			}
			err = db.Save(newPolyTransactions).Error
			if err != nil {
				panic(err)
			}
			count++
			time.Sleep(time.Second * 5)
		} else {
			break
		}
	}
	countTables("mchain_tx", "poly_transactions", exp, db)
}

func migrateExplorerDstTransactions(exp, db *gorm.DB) {
	logs.Info("Runnign migrateExplorerDstTransactions")
	selectNum := 1000
	count := 0
	for true {
		logs.Info("migrateExplorerDstTransactions %d", count)
		dstTransactions := make([]*explorerdao.DstTransaction, 0)
		//exp.Preload("DstTransfer").Order("tt asc").Limit(selectNum).Find(&dstTransactions)
		err := exp.Preload("DstTransfer").Limit(selectNum).Offset(selectNum * count).Order("tt asc").Find(&dstTransactions).Error
		if err != nil {
			panic(err)
		}
		if len(dstTransactions) > 0 {
			dstTransactionsJson, err := json.Marshal(dstTransactions)
			if err != nil {
				panic(err)
			}
			newDstTransactions := make([]*models.DstTransaction, 0)
			err = json.Unmarshal(dstTransactionsJson, &newDstTransactions)
			if err != nil {
				panic(err)
			}
			for _, transaction := range newDstTransactions {
				if transaction.DstTransfer != nil {
					transaction.DstTransfer.From = AddressAsHash(transaction.DstTransfer.ChainId, transaction.DstTransfer.From)
					transaction.DstTransfer.To = AddressAsHash(transaction.DstTransfer.ChainId, transaction.DstTransfer.To)
				}
			}
			err = db.Save(newDstTransactions).Error
			if err != nil {
				panic(err)
			}
			count++
			time.Sleep(time.Second * 5)
		} else {
			break
		}
	}
	countTables("tchain_tx", "dst_transactions", exp, db)
}

func migrateTableInBatches(orderKey string, src, db *gorm.DB, table string, model func() interface{}, query func(*gorm.DB) *gorm.DB) {
	logs.Info("Runnign migrate table in batch %s", table)
	selectNum := 1000
	count := 0
	order := fmt.Sprintf("%s asc", orderKey)
	for {
		logs.Info("%s %d", table, count)
		entries := model()
		res := query(src).Limit(selectNum).Offset(selectNum * count).Order(order).Find(entries)
		checkError(res.Error, "Fetch src_transactions")
		if res.RowsAffected > 0 {
			err := db.Save(entries).Error
			checkError(err, "Save src_transactions")
			count++
			time.Sleep(time.Second * 1)
		} else {
			break
		}
	}
	countTables(table, table, src, db)
}

func migrateBridgeTxs(bri, db *gorm.DB) {
	{
		model := func() interface{} { return &[]*models.SrcTransaction{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx.Preload("SrcTransfer")
		}
		migrateTableInBatches("time", bri, db, "src_transactions", model, query)
	}
	{
		model := func() interface{} { return &[]*models.PolyTransaction{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx
		}
		migrateTableInBatches("time", bri, db, "poly_transactions", model, query)
	}
	{
		model := func() interface{} { return &[]*models.DstTransaction{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx.Preload("DstTransfer")
		}
		migrateTableInBatches("time", bri, db, "dst_transactions", model, query)
	}
	{
		model := func() interface{} { return &[]*models.WrapperTransaction{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx
		}
		migrateTableInBatches("time", bri, db, "wrapper_transactions", model, query)
	}
	{
		model := func() interface{} { return &[]*models.SrcSwap{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx
		}
		migrateTableInBatches("time", bri, db, "src_swaps", model, query)
	}
	{
		model := func() interface{} { return &[]*models.DstSwap{} }
		query := func(tx *gorm.DB) *gorm.DB {
			return tx
		}
		migrateTableInBatches("time", bri, db, "dst_swaps", model, query)
	}
}

func verifyTables(bri, db *gorm.DB) {
	assert := func(check bool) {
		if !check {
			panic("false")
		}
	}

	limit := 200
	tsp := time.Now().Unix() - 60*60*24*1
	{
		data := []*models.SrcTransaction{}
		err := bri.Where("time < ?", tsp).Order("time desc").Limit(limit).Preload("SrcTransfer").Preload("SrcSwap").Find(&data).Error
		checkError(err, "Loading data")
		for _, a := range data {
			b := models.SrcTransaction{}
			err := db.Where("hash = ? ", a.Hash).Preload("SrcTransfer").Preload("SrcSwap").First(&b).Error
			checkError(err, "Loading data")
			assert(reflect.DeepEqual(a, b))
		}
	}
	{
		data := []*models.PolyTransaction{}
		err := bri.Where("time < ?", tsp).Order("time desc").Limit(limit).Find(&data).Error
		checkError(err, "Loading data")
		for _, a := range data {
			b := models.PolyTransaction{}
			err := db.Where("hash = ? ", a.Hash).First(&b).Error
			checkError(err, "Loading data")
			assert(reflect.DeepEqual(a, b))
		}
	}
	{
		data := []*models.DstTransaction{}
		err := bri.Where("time < ?", tsp).Order("time desc").Limit(limit).Preload("DstTransfer").Preload("DstSwap").Find(&data).Error
		checkError(err, "Loading data")
		for _, a := range data {
			b := models.DstTransaction{}
			err := db.Where("hash = ? ", a.Hash).Preload("DstTransfer").Preload("DstSwap").First(&b).Error
			checkError(err, "Loading data")
			assert(reflect.DeepEqual(a, b))
		}
	}
}
