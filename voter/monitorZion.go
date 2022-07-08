package voter

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/contracts/native/cross_chain_manager/common"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/signature_manager_abi"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/polynetwork/neo3-voter/zion"
)

var ZionUsefulBlockNum = uint64(1)

func (v *Voter) pickZionClient() *zion.ZionTools {
	v.zidx = randIdx(len(v.zionClients))
	return v.zionClients[v.zidx]
}

func (v *Voter) getZionStartHeight() (startHeight uint64) {
	startHeight = v.config.ForceConfig.ZionStartHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetZionHeight()
	if startHeight > 0 {
		return
	}

RETRY:
	startHeight, err := v.pickZionClient().GetNodeHeight()
	if err != nil {
		Log.Fatalf("zion GetNodeHeight failed: %v", err)
		goto RETRY
	}
	return
}

func (v *Voter) monitorZion() {
	nextHeight := v.getZionStartHeight()

	for {
		height, err := v.zionClients[v.zidx].GetNodeHeight()
		if err != nil {
			Log.Errorf("zion GetNodeHeight error: %v", err)
			continue
		}
		height--
		if height < nextHeight+ZionUsefulBlockNum {
			//Log.Infof("monitorPoly height(%d) < nextHeight(%d)+POLY_USEFUL_BLOCK_NUM(%d)", height, nextHeight, PolyUsefulBlockNum)
			continue
		}

		for nextHeight < height-ZionUsefulBlockNum {
			Log.Infof("handling zion height: %d", nextHeight)
			err = v.handleMakeTxEvents(nextHeight)
			if err != nil {
				Log.Warnf("handleMakeTxEvents err: %v", err)
				sleep()
				continue
			}
			nextHeight++
		}

		err = v.bdb.PutZionHeight(nextHeight)
		if err != nil {
			Log.Warnf("PutZionHeight failed: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

func (v *Voter) handleMakeTxEvents(height uint64) error {
	opt := &bind.FilterOpts{
		Start:   height,
		End:     &height,
		Context: context.Background(),
	}
	contract := v.zionCCMs[v.zidx]
	events, err := contract.FilterMakeProof(opt)
	if err != nil {
		return fmt.Errorf("FilterMakeProof error: %v", err)
	}

	if events == nil {
		return nil
	}

	empty := true
	for events.Next() {
		evt := events.Event
		if evt.Raw.Address != v.zionCcmAddr {
			Log.Warnf("event source contract invalid: %s, expect: %s, height: %d",
				evt.Raw.Address.Hex(), v.zionCcmAddr.Hex(), height)
			continue
		}
		empty = false
		tmv := new(common.ToMerkleValue)
		value, err := hex.DecodeString(evt.MerkleValueHex)
		if err != nil {
			return fmt.Errorf("decode MerkleValueHex error: %v", err)
		}
		err = rlp.DecodeBytes(value, tmv)
		if err != nil {
			return fmt.Errorf("rlp.DecodeBytes error: %v", err)
		}

		sig, err := v.signForNeo(value) // rlp encoded ToMerkleValue
		if err != nil {
			return fmt.Errorf("signForNeo error: %v", err)
		}

		txHash, err := v.makeZionTx(utils.SignatureManagerContractAddress,
			signature_manager_abi.SignatureManagerABI,
			"addSignature",
			height,
			value,
			sig)
		if err != nil {
			return fmt.Errorf("commitSig error: %v", err)
		}

		err = v.waitTx(txHash)
		if err != nil {
			return fmt.Errorf("handleMakeTxEvents e: %v", err)
		}
		Log.Infof("handleMakeTxToNeo, zion height: %d, new txHash: %s", height, txHash)
	}

	Log.Infof("zion height %d empty: %v", height, empty)
	return nil
}

func (v *Voter) signForNeo(data []byte) (sig []byte, err error) {
	sig, err = v.pair.Sign(data)
	return
}
