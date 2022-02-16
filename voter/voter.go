package voter

import (
	"fmt"
	"github.com/joeqian10/neo3-gogogo/keys"
	"github.com/joeqian10/neo3-gogogo/rpc"
	"github.com/polynetwork/neo3-voter/config"
	"github.com/polynetwork/neo3-voter/db"
	"github.com/polynetwork/neo3-voter/log"
	sdk "github.com/polynetwork/poly-go-sdk"
	"github.com/polynetwork/poly/core/types"
	"time"
)

var Log = log.Log

type Voter struct {
	polySdk *sdk.PolySdk
	signer  *sdk.Account
	config  *config.Config
	clients []*rpc.RpcClient
	idx     int
	pair    *keys.KeyPair

	neoStateRootHeight uint32

	bdb *db.BoltDB
}

func New(polySdk *sdk.PolySdk, signer *sdk.Account, conf *config.Config) *Voter {
	return &Voter{polySdk: polySdk, signer: signer, config: conf}
}

func (v *Voter) init() (err error) {
	// use poly's private key and neo's hash and curve
	pkBytes := polyPrivateKey2Hex(v.signer.PrivateKey)
	pair, err := keys.NewKeyPair(pkBytes)
	if err != nil {
		return
	}
	v.pair = pair
	// fill neo clients
	for _, url := range v.config.NeoConfig.RpcUrlList {
		c := rpc.NewClient(url)
		v.clients = append(v.clients, c)
	}
	v.neoStateRootHeight = 0
	// add db
	bdb, err := db.NewBoltDB(v.config.BoltDbPath)
	if err != nil {
		return
	}
	v.bdb = bdb

	return
}

func (v *Voter) Start() {
	err := v.init()
	if err != nil {
		Log.Fatalf("Voter.init failed: %v", err)
	}

	go v.monitorNeo()
	go v.monitorPoly()
}

func (v *Voter) waitTx(txHash string) (err error) {
	start := time.Now()
	var tx *types.Transaction
	for {
		tx, err = v.polySdk.GetTransaction(txHash)
		if tx == nil || err != nil {
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
