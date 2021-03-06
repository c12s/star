package main

import (
	"context"
	"fmt"
	"github.com/c12s/star/syncer/nats"
	actor "github.com/c12s/starsystem"
	sg "github.com/c12s/stellar-go"
	"runtime"
	"time"
)

const (
	Configs = "configs"
	Actions = "actions"
	Secrets = "secrets"
)

func main() {
	config, err := ConfigFile()
	if err != nil {
		fmt.Println(err)
		return
	}

	sync, err2 := nats.NewNatsSync(config.Flusher)
	if err2 != nil {
		fmt.Println(err2)
		return
	}

	uploader, err3 := nats.NewNatsUploader(config.Flusher, config.NodeId, config.STopic, config.ErrTopic)
	if err2 != nil {
		fmt.Println(err3)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	n, err := sg.NewCollector(config.InstrumentConf["address"], config.InstrumentConf["stopic"])
	if err != nil {
		fmt.Println(err)
		return
	}
	c, err := sg.InitCollector(config.InstrumentConf["location"], n)
	if err != nil {
		fmt.Println(err)
		return
	}
	go c.Start(ctx, 15*time.Second)

	star := NewStar(config, sync)
	star.Start(
		map[string]actor.Actor{
			Configs: ConfigsActor{uploader: uploader},
			Secrets: SecretsActor{uploader: uploader},
			Actions: ActionsActor{uploader: uploader},
		})

	fmt.Println("Starting project star...")
	runtime.Goexit()
	star.Stop()
	cancel()
}
