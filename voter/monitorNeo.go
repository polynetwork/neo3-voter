package voter

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/info_sync_abi"
	"github.com/ethereum/go-ethereum/contracts/native/info_sync"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	"github.com/ethereum/go-ethereum/core/types"
	ethCrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/joeqian10/neo3-gogogo/block"
	"github.com/joeqian10/neo3-gogogo/io"
	"github.com/joeqian10/neo3-gogogo/rpc"
	"github.com/joeqian10/neo3-gogogo/rpc/models"
	"github.com/polynetwork/neo3-voter/zion"
)

const (
	SYNC_BLOCK_HEADER     = "syncBlockHeader"
	IMPORT_OUTER_TRANSFER = "importOuterTransfer"
	SYNC_ROOT_INFO		  = "syncRootInfo"
	EMPTY                 = ""
)

var NeoUsefulBlockNum = uint64(1)

func (v *Voter) pickNeoClient() *rpc.RpcClient {
	v.idx = randIdx(len(v.zionClients))
	return v.neoClients[v.idx]
}

func (v *Voter) pickZionClient() *zion.ZionTools {
	v.zidx = randIdx(len(v.zionClients))
	return v.zionClients[v.zidx]
}

func (v *Voter) getNeoBlockHeader(hashOrIndex string) models.RpcBlockHeader {
	c := v.neoClients[v.idx] // use the reliable one
	for {
		response := c.GetBlockHeader(hashOrIndex)
		if response.HasError() {
			Log.Errorf("neoSdk.GetBlock error: %s, client: %s", response.GetErrorInfo(), c.Endpoint.String())
			c = v.pickNeoClient()
			time.Sleep(10 * time.Second)
			continue
		}
		h := response.Result
		if h.Hash == EMPTY {
			Log.Errorf("neoSdk.GetBlock response is empty, client: %s", response.GetErrorInfo(), c.Endpoint.String())
			c = v.pickNeoClient()
			time.Sleep(10 * time.Second)
			continue
		}
		return h
	}
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
		sleep()
		goto RPC
	}
	startHeight = uint64(response.Result - 1)
	return
}

// GetLatestSyncHeightOnZion - get the synced NEO blockHeight from zion
func (v *Voter) GetLatestSyncHeightOnZion(neoChainID uint64) (uint64, error) {
	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], neoChainID)
GETSTORAGE:
	c := v.pickZionClient()
	heightBytes, err := c.GetStorage(utils.InfoSyncContractAddress, append([]byte(info_sync.CURRENT_HEIGHT), id[:]...))
	if err != nil {
		Log.Errorf("getStorage error: %v, retrying", err)
		sleep()
		goto GETSTORAGE
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
		h := v.getNeoBlockHeader(strconv.FormatUint(nextHeight-1, 10))
		v.neoNextConsensus = h.NextConsensus // record the previous peers
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
			err := v.syncHeaderToZion(nextHeight)
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
			Log.Errorf("db.PutNeoHeight error: %v", err)
		}
		time.Sleep(time.Second * 15)
	}
}

func (v *Voter) syncHeaderToZion(height uint64) error {
	latestHeight, err := v.GetLatestSyncHeightOnZion(v.config.NeoConfig.SideChainId)
	if height <= latestHeight {
		return nil
	}

	rpcBH := v.getNeoBlockHeader(strconv.FormatUint(height, 10))
	blockHeader, err := block.NewBlockHeaderFromRPC(&rpcBH)
	if err != nil {
		return fmt.Errorf("NewBlockHeaderFromRPC error: %v", err)
	}
	// serialize
	buff := io.NewBufBinaryWriter()
	blockHeader.Serialize(buff.BinaryWriter)
	header := buff.Bytes()

	info := &info_sync.RootInfo{
		Height: uint32(height),
		Info:   header,
	}

	headerData, err := rlp.EncodeToBytes(info)
	if err != nil {
		return fmt.Errorf("rlp.EncodeToBytes(info) error: %v", err)
	}

	infos := [][]byte{headerData}
	param := info_sync.SyncRootInfoParam{
		ChainID:   v.config.NeoConfig.SideChainId,
		RootInfos: infos,
	}
	digest, err := param.Digest()
	if err != nil {
		return fmt.Errorf("param.Digest() error: %v", err)
	}

	param.Signature, err = ethCrypto.Sign(digest, v.signer.PrivateKey)
	if err != nil {
		return fmt.Errorf("ethCrypto.Sign error: %v", err)
	}

	txHash, err := v.makeZionTx(utils.InfoSyncContractAddress,
		info_sync_abi.IInfoSyncABI,
		SYNC_ROOT_INFO,
		v.config.NeoConfig.SideChainId, // chainID
		infos,							// rootInfos
		param.Signature)				// signature
	if err != nil {
		return fmt.Errorf("makeZionTx error: %v", err)
	}
	err = v.waitTx(txHash)
	if err != nil {
		Log.Errorf("waitTx error: %v, zionTxHash: %s", err, txHash)
		return err
	}
	Log.Infof("syncHeaderToZion done, zionTxHash: %v", txHash)
	return nil
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
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		To:       &contractAddress,
		Value:    big.NewInt(0),
		Data:     data,
	})
	s := types.LatestSignerForChainID(v.zionChainId)

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
