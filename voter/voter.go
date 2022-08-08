package voter

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/joeqian10/neo3-gogogo/keys"
	"github.com/joeqian10/neo3-gogogo/rpc"
	"github.com/polynetwork/neo3-voter/config"
	"github.com/polynetwork/neo3-voter/db"
	"github.com/polynetwork/neo3-voter/log"
	"github.com/polynetwork/neo3-voter/zion"
)

var Log = log.Log

type Voter struct {
	zionChainId *big.Int
	signer      *zion.ZionSigner
	zionClients []*zion.ZionTools
	//zionCCMs    []*cross_chain_manager_abi.CrossChainManager
	//zionCcmAddr common.Address
	zidx        int

	config             *config.Config
	neoClients         []*rpc.RpcClient // neo rpc client
	idx                int
	pair               *keys.KeyPair
	neoNextConsensus   string
	neoStateRootHeight uint64

	bdb *db.BoltDB
}

func NewVoter(conf *config.Config) *Voter {
	return &Voter{config: conf}
}

func (v *Voter) init() (err error) {
	// create a zion signer
	signer, err := zion.NewZionSigner(v.config.ZionConfig.NodeKey)
	if err != nil {
		panic(any(err))
	}
	v.signer = signer

	// use zion's private key and neo's hash and curve
	//pkBytes := zionPrivateKey2Hex(v.signer.PrivateKey)
	//pair, err := keys.NewKeyPair(pkBytes)
	//if err != nil {
	//	return
	//}
	//v.pair = pair

	// fill neo clients
	for _, url := range v.config.NeoConfig.RpcUrlList {
		c := rpc.NewClient(url)
		v.neoClients = append(v.neoClients, c)
	}
	v.neoStateRootHeight = 0

	// fill zion clients
	for _, url := range v.config.ZionConfig.RestUrlList {
		t := zion.NewZionTools(url)
		v.zionClients = append(v.zionClients, t)
	}

	// check chain id
	start := time.Now()
	chainID, err := v.zionClients[0].GetChainID() // use the first one
	if err != nil {
		panic(any("zionSdk.GetChainID " + err.Error()))
	}
	v.zionChainId = chainID
	Log.Infof("GetChainID() took %v", time.Now().Sub(start).String())

	// add db
	path := v.config.BoltDbPath
	if _, err := os.Stat(path); err != nil {
		Log.Infof("db path: %s does not exist, make dir", path)
		err := os.MkdirAll(path, 0711)
		if err != nil {
			return err
		}
	}
	bdb, err := db.NewBoltDB(path)
	if err != nil {
		return
	}
	v.bdb = bdb

	// ccm
	//v.zionCcmAddr = common.HexToAddress(v.config.ZionConfig.ECCMAddress)
	//for _, z := range v.zionClients {
	//	t, err := cross_chain_manager_abi.NewCrossChainManager(v.zionCcmAddr, z.GetEthClient())
	//	if err != nil {
	//		return fmt.Errorf("NewCrossChainManager error: %v", err)
	//	}
	//	v.zionCCMs = append(v.zionCCMs, t)
	//}

	return
}

func (v *Voter) Start() {
	err := v.init()
	if err != nil {
		Log.Fatalf("Voter.init failed: %v", err)
		panic(any(err))
	}
	var wg sync.WaitGroup
	GoFunc(&wg, v.monitorNeo)
	//GoFunc(&wg, v.monitorZion) // neo3-relayer should do this
	wg.Wait()
}

func (v *Voter) waitTx(txHash string) (err error) {
	start := time.Now()
	for {
		duration := time.Second * 30
		timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
		receipt, err := v.zionClients[v.zidx].GetEthClient().TransactionReceipt(timerCtx, common.HexToHash(txHash))
		cancelFunc()
		if receipt == nil || err != nil {
			if time.Since(start) > time.Minute*5 {
				err = fmt.Errorf("waitTx timeout")
				return
			}
			time.Sleep(time.Second)
			continue
		}
		return
	}
}
