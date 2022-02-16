package main

import (
	"fmt"
	"github.com/polynetwork/neo3-voter/voter"
	"github.com/polynetwork/poly/core/types"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/polynetwork/neo3-voter/cmd"
	"github.com/polynetwork/neo3-voter/common"
	"github.com/polynetwork/neo3-voter/config"
	"github.com/polynetwork/neo3-voter/log"

	sdk "github.com/polynetwork/poly-go-sdk"
	"github.com/urfave/cli"
)

var Log = log.Log

func setupApp() *cli.App {
	app := cli.NewApp()
	app.Usage = "NEO3 Voter"
	app.Action = start
	app.Copyright = "Copyright in 2022 The NEO Project"
	app.Flags = []cli.Flag{
		cmd.ConfigPathFlag,
		cmd.PolyPwd,
	}
	app.Commands = []cli.Command{}
	app.Before = func(context *cli.Context) error {
		runtime.GOMAXPROCS(runtime.NumCPU())
		return nil
	}
	return app
}

func main() {
	if err := setupApp().Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(ctx *cli.Context) {
	configPath := ctx.String(cmd.GetFlagName(cmd.ConfigPathFlag))
	err := config.DefConfig.Init(configPath)
	if err != nil {
		fmt.Println("DefConfig.Init error: ", err)
		return
	}

	polyPwd := ctx.GlobalString(cmd.GetFlagName(cmd.PolyPwd))

	//create poly RPC Client
	polySdk := sdk.NewPolySdk()
	err = SetUpPoly(polySdk, config.DefConfig.PolyConfig.RpcUrl)
	if err != nil {
		panic(fmt.Errorf("failed to set up poly: %v", err))
	}

	// Get wallet account for poly
	signer, ok := common.GetAccountByPassword(polySdk, config.DefConfig.PolyConfig.WalletFile, polyPwd)
	if !ok {
		Log.Errorf("[NEO Relayer] common.GetAccountByPassword error")
		return
	}

	Log.Infof("voter %s", signer.Address.ToBase58())
	v := voter.New(polySdk, signer, config.DefConfig)
	v.Start()

	waitToExit()
}

func waitToExit() {
	exit := make(chan bool, 0)
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range sc {
			Log.Infof("Neo Relayer received exit signal: %v.", sig.String())
			close(exit)
			break
		}
	}()
	<-exit
}

func SetUpPoly(poly *sdk.PolySdk, rpcAddr string) error {
	poly.NewRpcClient().SetAddress(rpcAddr)
	c1 := make(chan *types.Header, 1)
	c2 := make(chan error, 1)

	// use another routine to check time out and error
	go func() {
		hdr, err := poly.GetHeaderByHeight(0)
		if err != nil {
			c2 <- err
		}
		c1 <- hdr
	}()

	select {
	case hdr := <- c1:
		poly.SetChainId(hdr.ChainID)
	case err := <- c2:
		return  err
	case <- time.After(time.Second * 5):
		return fmt.Errorf("poly rpc port timeout")
	}

	return nil
}
