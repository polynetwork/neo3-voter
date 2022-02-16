package voter

import (
	"fmt"
	"github.com/joeqian10/neo3-gogogo/crypto"
	"github.com/joeqian10/neo3-gogogo/helper"
	"github.com/joeqian10/neo3-gogogo/io"
	"github.com/joeqian10/neo3-gogogo/mpt"
	"github.com/joeqian10/neo3-gogogo/rpc"
	"github.com/joeqian10/neo3-gogogo/rpc/models"
	"github.com/polynetwork/neo3-voter/common"
	pCommon "github.com/polynetwork/poly/common"
	hsCommon "github.com/polynetwork/poly/native/service/header_sync/common"
	"github.com/polynetwork/poly/native/service/header_sync/neo"
	polyUtils "github.com/polynetwork/poly/native/service/utils"
	"strconv"
	"strings"
	"time"
)

const EMPTY = ""

var NeoUsefulBlockNum = uint32(1)

func (v *Voter) getNeoStartHeight() (startHeight uint32) {
	startHeight = v.config.ForceConfig.NeoStartHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetNeoHeight()
	if startHeight > 0 {
		return
	}

RPC:
	c := v.chooseClient()
	response := c.GetBlockCount()
	if response.HasError() {
		Log.Errorf("GetBlockCount error: %s, client: %s", response.GetErrorInfo(), c.Endpoint.String())
		goto RPC
	}
	startHeight = uint32(response.Result - 1)
	return
}

func (v *Voter) monitorNeo() {

	nextHeight := v.getNeoStartHeight()

	for {
		c := v.chooseClient()
		response := c.GetBlockCount()
		if response.HasError() {
			Log.Warnf("GetBlockCount failed: %s", response.GetErrorInfo())
			sleep()
			continue
		}
		height := uint32(response.Result - 1)
		if height < nextHeight+NeoUsefulBlockNum {
			sleep()
			continue
		}

		for nextHeight < height-NeoUsefulBlockNum {
			Log.Infof("process neo height: %d", nextHeight)
			err := v.fetchLockDepositEvents(nextHeight)
			if err != nil {
				Log.Warnf("fetchLockDepositEvents failed:%v", err)
				sleep()
				continue
			}
			nextHeight++
		}
		time.Sleep(time.Second * 2)
	}
}

func (v *Voter) fetchLockDepositEvents(height uint32) error {
	c := v.chooseClient()
	blockResponse := c.GetBlock(strconv.Itoa(int(height)))
	if blockResponse.HasError() {
		return fmt.Errorf("neoSdk.GetBlockByIndex error: %s", blockResponse.GetErrorInfo())
	}
	blk := blockResponse.Result
	if blk.Hash == "" {
		return fmt.Errorf("neoSdk.GetBlockByIndex error: empty block")
	}

	txs := blk.Tx
	for _, tx := range txs {
		// check tx script is useless since which contract calling ccmc is not sure
		response := c.GetApplicationLog(tx.Hash)
		if response.HasError() {
			return fmt.Errorf("neoSdk.GetApplicationLog error: %s", response.GetErrorInfo())
		}

		for _, execution := range response.Result.Executions {
			if execution.VMState == "FAULT" { // skip fault transactions
				continue
			}
			notifications := execution.Notifications
			// this loop confirm tx is a cross chain tx
			for _, notification := range execution.Notifications {
				u, _ := helper.UInt160FromString(notification.Contract)
				if "0x"+u.String() == v.config.NeoConfig.CCMC && notification.EventName == "CrossChainLockEvent" {
					if notification.State.Type != "Array" {
						return fmt.Errorf("notification.State.Type error: Type is not Array")
					}
					notification.State.Convert() // Type == "Array"
					// convert to []InvokeStack
					states := notification.State.Value.([]models.InvokeStack)
					if len(states) != 5 {
						return fmt.Errorf("notification.State.Value error: Wrong length of states")
					}
					// when empty, relay everything
					if v.config.NeoConfig.NtorContract != "" {
						// this loop check it is for this specific contract
						for index, ntf := range notifications {
							nc, _ := helper.UInt160FromString(ntf.Contract)
							if "0x"+nc.String() != v.config.NeoConfig.NtorContract {
								if index < len(notifications)-1 {
									continue
								}
								Log.Infof("This cross chain tx is not for this specific contract.")
								goto NEXT
							} else {
								break
							}
						}
					}
					key := states[3].Value.(string)       // base64 string for storeKey: 0102 + toChainId + toRequestId, like 01020501
					temp, err := crypto.Base64Decode(key) // base64 encoded
					if err != nil {
						return fmt.Errorf("base64decode key error: %s", err)
					}
					key = helper.BytesToHex(temp)
					//get relay chain sync height
					latestSyncHeight, err := v.GetLatestSyncHeightOnPoly(v.config.NeoConfig.SideChainId)
					if err != nil {
						return fmt.Errorf("GetCurrentRelayChainSyncHeight error: %s", err)
					}
					var passed uint32
					if height >= latestSyncHeight {
						passed = height
					} else {
						passed = latestSyncHeight
					}
					Log.Infof("process neo tx: " + tx.Hash)
					txHash, err := v.commitVote(key, passed)
					if err != nil {
						Log.Errorf("--------------------------------------------------")
						Log.Errorf("commitVote error: %s", err)
						Log.Errorf("neoHeight: %d, neoTxHash: %s", height, tx.Hash)
						Log.Errorf("--------------------------------------------------")
						return err
					}
					if txHash == EMPTY {
						continue
					}
					err = v.waitTx(txHash)
					if err != nil {
						Log.Errorf("waitTx failed: %v, txHash: %s", err, txHash)
						return err
					}
				}
			NEXT:
			} // notification
		} // execution
	}
	return nil
}

// GetLatestSyncHeightOnPoly :get the synced NEO blockHeight from poly
func (v *Voter) GetLatestSyncHeightOnPoly(neoChainID uint64) (uint32, error) {
	contractAddress := polyUtils.HeaderSyncContractAddress
	neoChainIDBytes := common.GetUint64Bytes(neoChainID)
	key := common.ConcatKey([]byte(hsCommon.CONSENSUS_PEER), neoChainIDBytes)
	value, err := v.polySdk.ClientMgr.GetStorage(contractAddress.ToHexString(), key)
	if err != nil {
		return 0, fmt.Errorf("getStorage error: %s", err)
	}
	neoConsensusPeer := new(neo.NeoConsensus)
	if err := neoConsensusPeer.Deserialization(pCommon.NewZeroCopySource(value)); err != nil {
		return 0, fmt.Errorf("neoconsensus peer deserialize err: %s", err)
	}

	height := neoConsensusPeer.Height
	height++
	return height, nil
}

func (v *Voter) commitVote(key string, height uint32) (string, error) {

	c := v.chooseClient()
	//get current state height
	var stateHeight uint32 = 0
	for stateHeight < height {
		res := c.GetStateHeight()
		if res.HasError() {
			return EMPTY, fmt.Errorf("neoSdk.GetStateHeight error: %s", res.GetErrorInfo())
		}
		stateHeight = res.Result.ValidateRootIndex
	}

	// get state root
	srGot := false
	var height2 uint32
	stateRoot := mpt.StateRoot{}
	if height >= v.neoStateRootHeight {
		height2 = height
	} else {
		height2 = v.neoStateRootHeight
	}
	for !srGot {
		res2 := c.GetStateRoot(height2)
		if res2.HasError() {
			return EMPTY, fmt.Errorf("neoSdk.GetStateRootByIndex error: %s", res2.GetErrorInfo())
		}
		stateRoot = res2.Result
		if len(stateRoot.Witnesses) == 0 { // no witness
			height2++
		} else {
			srGot = true
			v.neoStateRootHeight = height2 // next tx can start from this height to get state root
		}
	}
	buff := io.NewBufBinaryWriter()
	stateRoot.Serialize(buff.BinaryWriter)
	crossChainMsg := buff.Bytes()
	//Log.Infof("stateroot: %s", helper.BytesToHex(crossChainMsg))

	// get proof
	res3 := c.GetProof(stateRoot.RootHash, v.config.NeoConfig.CCMC, crypto.Base64Encode(helper.HexToBytes(key)))
	if res3.HasError() {
		return EMPTY, fmt.Errorf("neoSdk.GetProof error: %s", res3.Error.Message)
	}
	proof, err := crypto.Base64Decode(res3.Result)
	if err != nil {
		return EMPTY, fmt.Errorf("decode proof error: %s", err)
	}
	//Log.Info("proof: %s", helper.BytesToHex(proof))

	// following is for testing only
	//id, k, proofs, err := mpt.ResolveProof(proof)
	//root, _ := helper.UInt256FromString(stateRoot.RootHash)
	//value, err := mpt.VerifyProof(root, id, k, proofs)
	//Log.Infof("value: %s", helper.BytesToHex(value))

	//sending SyncProof transaction to
	txHash, err := v.polySdk.Native.Ccm.ImportOuterTransfer(
		v.config.NeoConfig.SideChainId,
		nil,
		height,
		proof,
		v.signer.Address[:],
		crossChainMsg,
		v.signer)
	if err != nil {
		if strings.Contains(err.Error(), "checkDoneTx, tx already done") {
			Log.Infof("ImportOuterTransfer: %s", err.Error())
			return EMPTY, nil
		} else {
			return EMPTY, fmt.Errorf("ImportOuterTransfer error: %s, crossChainMsg: %s, proof: %s", err, helper.BytesToHex(crossChainMsg), helper.BytesToHex(proof))
		}
	}

	return txHash.ToHexString(), nil
}

func (v *Voter) chooseClient() *rpc.RpcClient {
	v.idx = randIdx(len(v.clients))
	return v.clients[v.idx]
}
