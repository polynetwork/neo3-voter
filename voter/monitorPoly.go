package voter

import (
	"encoding/hex"
	"github.com/polynetwork/poly-go-sdk/common"
	common1 "github.com/polynetwork/poly/common"
	common2 "github.com/polynetwork/poly/native/service/cross_chain_manager/common"
	"time"
)

var PolyUsefulBlockNum = uint32(1)

func (v *Voter) getPolyStartHeight() (startHeight uint32) {
	startHeight = v.config.ForceConfig.PolyStartHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetPolyHeight()
	if startHeight > 0 {
		return
	}

	startHeight, err := v.polySdk.GetCurrentBlockHeight()
	if err != nil {
		Log.Fatalf("polySdk.GetCurrentBlockHeight failed:%v", err)
	}
	return
}

func (v *Voter) monitorPoly() {

	nextHeight := v.getPolyStartHeight()

	for {
		height, err := v.polySdk.GetCurrentBlockHeight()
		if err != nil {
			Log.Errorf("monitorPoly GetCurrentBlockHeight failed:%v", err)
			continue
		}
		height--
		if height < nextHeight+PolyUsefulBlockNum {
			//Log.Infof("monitorPoly height(%d) < nextHeight(%d)+POLY_USEFUL_BLOCK_NUM(%d)", height, nextHeight, PolyUsefulBlockNum)
			continue
		}

		for nextHeight < height-PolyUsefulBlockNum {
			Log.Infof("handling poly height:%d", nextHeight)
			err = v.handleMakeTxEvents(nextHeight)
			if err != nil {
				Log.Warnf("fetchLockDepositEvents failed:%v", err)
				sleep()
				continue
			}
			nextHeight++
		}
		Log.Infof("monitorPoly nextHeight:%d", nextHeight)
		err = v.bdb.PutPolyHeight(nextHeight)
		if err != nil {
			Log.Warnf("PutPolyHeight failed:%v", err)
		}
		time.Sleep(time.Second * 2)
	}
}

func (v *Voter) handleMakeTxEvents(height uint32) (err error) {

	hdr, err := v.polySdk.GetHeaderByHeight(height + 1)
	if err != nil {
		return
	}
	events, err := v.polySdk.GetSmartContractEventByBlock(height)
	if err != nil {
		return
	}

	empty := true

	for _, event := range events {
		for _, notify := range event.Notify {
			if notify.ContractAddress == v.config.PolyConfig.EntranceContractAddress {
				states := notify.States.([]interface{})
				method, _ := states[0].(string)
				if method != "makeProof" {
					continue
				}

				if uint64(states[2].(float64)) != v.config.NeoConfig.SideChainId {
					continue
				}
				empty = false
				var proof *common.MerkleProof
				proof, err = v.polySdk.GetCrossStatesProof(hdr.Height-1, states[5].(string))
				if err != nil {
					Log.Errorf("handleMakeTxEvents - failed to get proof for key %s: %v", states[5].(string), err)
					return
				}
				auditpath, _ := hex.DecodeString(proof.AuditPath)
				value, _, _, _ := parseAuditpath(auditpath)
				param := &common2.ToMerkleValue{}
				if err = param.Deserialization(common1.NewZeroCopySource(value)); err != nil {
					Log.Errorf("handleDepositEvents - failed to deserialize MakeTxParam (value: %x, err: %v)", value, err)
					return
				}
				// sign toMerkleValue
				var sig []byte
				sig, err = v.signForNeo(value)
				if err != nil {
					Log.Errorf("signForNeo failed:%v", err)
					return
				}

				var txHash string
				txHash, err = v.commitSig(height, value, sig)
				if err != nil {
					Log.Errorf("signForNeo failed:%v", err)
					return
				}
				err = v.waitTx(txHash)
				if err != nil {
					Log.Errorf("handleMakeTxEvents failed:%v", err)
					return
				}
			}
		}
	}

	Log.Infof("poly height %d empty: %v", height, empty)
	return
}

func (v *Voter) signForNeo(data []byte) (sig []byte, err error) {
	sig, err = v.pair.Sign(data)
	return
}

func (v *Voter) commitSig(height uint32, subject, sig []byte) (txHash string, err error) {

	hash, err := v.polySdk.Native.Sm.AddSignature(v.config.NeoConfig.SideChainId, subject, sig, v.signer)
	if err != nil {
		return
	}

	txHash = hash.ToHexString()
	Log.Infof("commitSig, height: %d, txhash: %s", height, txHash)
	return
}
