package main

import (
	"fmt"
	"github.com/polynetwork/neo3-voter/cmd"
	"github.com/polynetwork/neo3-voter/config"
	"github.com/polynetwork/neo3-voter/log"
	"github.com/polynetwork/neo3-voter/voter"
	"os"
	"os/signal"
	"runtime"
	"syscall"

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
	v := voter.NewVoter(config.DefConfig)
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
