package voter

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/cross_chain_manager_abi"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/header_sync_abi"
	common2 "github.com/ethereum/go-ethereum/contracts/native/header_sync/common"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/joeqian10/neo3-gogogo/block"
	"github.com/joeqian10/neo3-gogogo/crypto"
	"github.com/joeqian10/neo3-gogogo/helper"
	"github.com/joeqian10/neo3-gogogo/io"
	"github.com/joeqian10/neo3-gogogo/mpt"
	"github.com/joeqian10/neo3-gogogo/rpc"
	"github.com/joeqian10/neo3-gogogo/rpc/models"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const EMPTY = ""

var NeoUsefulBlockNum = uint64(1)

func (v *Voter) pickNeoClient() *rpc.RpcClient {
	v.idx = randIdx(len(v.zionClients))
	return v.neoClients[v.idx]
}

func (v *Voter) getNeoBlockHeader() {

}

func (v *Voter) getNeoStartHeight() (startHeight uint64) {
	startHeight = v.config.ForceConfig.NeoStartHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetNeoHeight()
	if startHeight > 0 {
		return
	}

RPC:
	c := v.pickNeoClient()
	response := c.GetBlockCount()
	if response.HasError() {
		Log.Errorf("GetBlockCount error: %s, client: %s", response.GetErrorInfo(), c.Endpoint.String())
		goto RPC
	}
	startHeight = uint64(response.Result - 1)
	return
}

// GetLatestSyncHeightOnZion - get the synced NEO blockHeight from zion
func (v *Voter) GetLatestSyncHeightOnZion(neoChainID uint64) (uint64, error) {
	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], neoChainID)
RETRY:
	c := v.pickZionClient()
	heightBytes, err := c.GetStorage(utils.HeaderSyncContractAddress, append([]byte(common2.CURRENT_HEADER_HEIGHT), id[:]...))
	if err != nil {
		Log.Errorf("getStorage error: %v, retrying", err)
		goto RETRY
	}
	if heightBytes == nil {
		return 0, fmt.Errorf("get side chain height failed, height store is nil")
	}
	var height uint64
	if len(heightBytes) > 7 {
		height = binary.LittleEndian.Uint64(heightBytes)
	} else if len(heightBytes) > 3 {
		height = uint64(binary.LittleEndian.Uint32(heightBytes))
	} else {
		err = fmt.Errorf("Failed to decode heightBytes, %v", heightBytes)
	}
	height++ // means the next
	return height, nil
}

func (v *Voter) monitorNeo() {
	nextHeight := v.getNeoStartHeight()

	c := v.neoClients[v.idx] // use the reliable one

	if nextHeight == 0 {
		v.neoNextConsensus = EMPTY
	} else {
	GETBLOCK:
		response := c.GetBlock(strconv.Itoa(int(nextHeight - 1))) // get the previous peers
		if response.HasError() {
			Log.Errorf("neoSdk.GetBlockByIndex error: %s, retrying", response.GetErrorInfo())
			c = v.pickNeoClient() // change to another client
			sleep()
			goto GETBLOCK
		}
		block := response.Result
		if block.Hash == "" {
			Log.Errorf("neoSdk.GetBlock response is empty, retrying")
			c = v.pickNeoClient() // change to another client
			sleep()
			goto GETBLOCK
		}
		v.neoNextConsensus = block.NextConsensus // record the previous peers
	}

	for {
		response := c.GetBlockCount()
		if response.HasError() {
			Log.Warnf("GetBlockCount failed: %s", response.GetErrorInfo())
			c = v.pickNeoClient() // change to another client
			sleep()
			continue
		}
		height := uint64(response.Result - 1)
		if height < nextHeight+NeoUsefulBlockNum {
			sleep()
			continue
		}

		for nextHeight < height-NeoUsefulBlockNum {
			Log.Infof("process neo height: %d", nextHeight)
			err := v.handleNeoLockEvents(nextHeight)
			if err != nil {
				Log.Errorf("handleNeoLockEvents error: %v, retrying", err)
				c = v.pickNeoClient() // change to another client
				sleep()
				continue
			}
			err = v.syncHeaderToZion(nextHeight)
			if err != nil {
				Log.Errorf("syncHeaderToZion error: %v, retrying", err)
				c = v.pickNeoClient() // change to another client
				sleep()
				continue
			}
			nextHeight++
		}
		err := v.bdb.PutNeoHeight(nextHeight)
		if err != nil {
			Log.Errorf("UpdateFlowHeight failed: %v", err)
		}
		time.Sleep(time.Second * 2)
	}
}

func (v *Voter) handleNeoLockEvents(height uint64) error {
	c := v.neoClients[v.idx]
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
					if v.config.NeoConfig.N2ZContract != "" {
						// this loop check it is for this specific contract
						for index, ntf := range notifications {
							nc, _ := helper.UInt160FromString(ntf.Contract)
							if "0x"+nc.String() != v.config.NeoConfig.N2ZContract {
								if index < len(notifications)-1 {
									continue
								}
								Log.Infof("This cross chain tx is not from the expected contract.")
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
					//get the neo chain synced height on zion
					latestSyncHeight, err := v.GetLatestSyncHeightOnZion(v.config.NeoConfig.SideChainId)
					if err != nil {
						return fmt.Errorf("GetCurrentRelayChainSyncHeight error: %s", err)
					}
					var passed uint64
					if height >= latestSyncHeight {
						passed = height
					} else {
						passed = latestSyncHeight
					}
					Log.Infof("process neo tx: %s", tx.Hash)
					txHash, err := v.syncProofToZion(key, passed)
					if err != nil {
						Log.Errorf("--------------------------------------------------")
						Log.Errorf("syncProofToZion error: %s", err)
						Log.Errorf("neoHeight: %d, neoTxHash: %s", height, tx.Hash)
						Log.Errorf("--------------------------------------------------")
						return err
					}
					err = v.waitTx(txHash)
					if err != nil {
						Log.Errorf("waitTx failed: %v, zionTxHash: %s", err, txHash)
						return err
					}
					Log.Infof("neo tx: %s handled, zion tx: %s", tx.Hash, txHash)
				}
			NEXT:
			} // notification
		} // execution
	}
	return nil
}

func (v *Voter) syncHeaderToZion(height uint64) error {
	latestHeight, err := v.GetLatestSyncHeightOnZion(v.config.NeoConfig.SideChainId)
	if height <= latestHeight {
		return nil
	}

	//Get NEO BlockHeader for syncing
	c := v.neoClients[v.idx]
GETBLOCKHEADER:
	response := c.GetBlockHeader(strconv.Itoa(int(height)))
	if response.HasError() {
		Log.Errorf("GetBlockHeader error: %v, retrying", response.GetErrorInfo())
		c = v.pickNeoClient() // change to another client
		sleep()
		goto GETBLOCKHEADER
	}
	rpcBH := response.Result
	if rpcBH.Hash == "" {
		Log.Errorf("GetBlockHeader response is empty, retrying")
		c = v.pickNeoClient() // change to another client
		sleep()
		goto GETBLOCKHEADER
	}
	if rpcBH.NextConsensus == v.neoNextConsensus {
		return nil
	}
	blockHeader, err := block.NewBlockHeaderFromRPC(&rpcBH)
	if err != nil {
		Log.Errorf("NewBlockHeaderFromRPC error: %v, retrying", response.GetErrorInfo())
		c = v.pickNeoClient()
		sleep()
		goto GETBLOCKHEADER
	}

	buff := io.NewBufBinaryWriter()
	blockHeader.Serialize(buff.BinaryWriter)
	header := buff.Bytes()

	txHash, err := v.makeZionTx(utils.HeaderSyncContractAddress,
		header_sync_abi.HeaderSyncABI,
		"syncBlockHeader",
		v.config.NeoConfig.SideChainId,
		v.signer.Address,
		[][]byte{header})
	if err != nil {
		return fmt.Errorf("makeZionTx error: %v", err)
	}
	err = v.waitTx(txHash)
	if err != nil {
		Log.Errorf("waitTx failed: %v, zionTxHash: %s", err, txHash)
		return err
	}
	Log.Infof("syncHeaderToZion done, zionTxHash: %v", txHash)
	return nil
}

func (v *Voter) syncProofToZion(key string, height uint64) (string, error) {
	c := v.neoClients[v.idx]
	//get current state height
	var stateHeight uint64 = 0
	for stateHeight < height {
		res := c.GetStateHeight()
		if res.HasError() {
			return EMPTY, fmt.Errorf("neoSdk.GetStateHeight error: %s", res.GetErrorInfo())
		}
		stateHeight = uint64(res.Result.ValidateRootIndex)
	}

	// get state root
	srGot := false
	var height2 uint64
	stateRoot := mpt.StateRoot{}
	if height >= v.neoStateRootHeight {
		height2 = height
	} else {
		height2 = v.neoStateRootHeight
	}
	for !srGot {
		res2 := c.GetStateRoot(uint32(height2))
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

	id, k, proofs, err := mpt.ResolveProof(proof)
	if err != nil {
		return EMPTY, fmt.Errorf("ResolveProof error: %s", err)
	}
	root, _ := helper.UInt256FromString(stateRoot.RootHash)
	value, err := mpt.VerifyProof(root, id, k, proofs)
	if err != nil {
		return EMPTY, fmt.Errorf("VerifyProof error: %s", err)
	}
	cctp, err := DeserializeCrossChainTxParameter(value, 0)
	if err != nil {
		return EMPTY, fmt.Errorf("DeserializeCrossChainTxParameter error: %s", err)
	}
	//Log.Infof("value: %s", helper.BytesToHex(value))

	// sending SyncProof transaction to zion
	zionHash, err := v.makeZionTx(utils.CrossChainManagerContractAddress,
		cross_chain_manager_abi.CrossChainManagerABI,
		"importOuterTransfer",
		v.config.NeoConfig.SideChainId,
		height,
		proof,
		v.signer.Address[:],
		crossChainMsg) // todo, arg position may be changed
	if err != nil {
		if strings.Contains(err.Error(), "tx already done") {
			Log.Infof("tx already imported, source tx hash: %s", helper.BytesToHex(cctp.TxHash))
			return EMPTY, nil
		} else {
			return EMPTY, fmt.Errorf("makeZionTx error: %v, height: %d, crossChainMsg: %s, proof: %s",
				err, height, helper.BytesToHex(crossChainMsg), helper.BytesToHex(proof))
		}
	}
	return zionHash, nil
}

func (v *Voter) makeZionTx(contractAddress common.Address, contractAbi string, method string, args ...interface{}) (string, error) {
	duration := time.Second * 30
	timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
	defer cancelFunc()

	ethCli := v.zionClients[v.zidx].GetEthClient()
	gasPrice, err := ethCli.SuggestGasPrice(timerCtx)
	if err != nil {
		return EMPTY, fmt.Errorf("SuggestGasPrice error: %v", err)
	}
	conAbi, err := abi.JSON(strings.NewReader(contractAbi))
	if err != nil {
		return EMPTY, fmt.Errorf("abi.JSON CrossChainManagerABI error: %v", err)
	}
	data, err := conAbi.Pack(method, args)
	if err != nil {
		return EMPTY, fmt.Errorf("pack zion tx data error: %v", err)
	}
	/*
		txHash, err := v.polySdk.Native.Ccm.ImportOuterTransfer(
			v.config.NeoConfig.SideChainId,
			nil,
			height,
			proof,
			v.signer.Address[:],
			crossChainMsg,
			v.signer)
		txHash, err := v.wallet.SendWithAccount(v.signer, utils.CrossChainManagerContractAddress, big.NewInt(0), 0, nil, nil, data)
	*/
	callMsg := ethereum.CallMsg{
		From:     v.signer.Address,
		To:       &contractAddress,
		Gas:      0,
		GasPrice: gasPrice,
		Value:    big.NewInt(0),
		Data:     data,
	}
	gasLimit, err := ethCli.EstimateGas(timerCtx, callMsg)
	if err != nil {
		return EMPTY, fmt.Errorf("EstimateGas error: %v", err)
	}

	nonce, err := ethCli.NonceAt(context.Background(), v.signer.Address, nil)
	if err != nil {
		return EMPTY, fmt.Errorf("NonceAt error: %v", err)
	}
	tx := types.NewTx(
		&types.LegacyTx{
			Nonce:    nonce,
			GasPrice: gasPrice,
			Gas:      gasLimit,
			To:       &contractAddress,
			Value:    big.NewInt(0),
			Data:     data,
		})
	s := types.LatestSignerForChainID(v.chainId)

	signedTx, err := types.SignTx(tx, s, v.signer.PrivateKey)
	if err != nil {
		return EMPTY, fmt.Errorf("SignTx error: %v", err)
	}
	err = ethCli.SendTransaction(timerCtx, signedTx)
	if err != nil {
		return EMPTY, fmt.Errorf("SendTransaction error: %v", err)
	}

	zionHash := signedTx.Hash().Hex()
	return zionHash, nil
}

func (v *Voter) getNonce(addr common.Address) uint64 {
	for {
		nonce, err := v.zionClients[v.zidx].GetEthClient().NonceAt(context.Background(), addr, nil)
		if err != nil {
			Log.Errorf("NonceAt error: %v", err)
			sleep()
			continue
		}
		return nonce
	}
}
