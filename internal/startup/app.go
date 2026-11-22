package startup

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	kuiperapi "github.com/c12s/kuiper/pkg/api"
	magnetarapi "github.com/c12s/magnetar/pkg/api"
	meridianapi "github.com/c12s/meridian/pkg/api"
	"github.com/c12s/star/internal/bootc"
	"github.com/c12s/star/internal/configs"
	"github.com/c12s/star/internal/servers"
	"github.com/c12s/star/internal/services"
	"github.com/c12s/star/internal/store"
	"github.com/c12s/star/internal/vm"
	"github.com/c12s/star/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type app struct {
	config               *configs.Config
	grpcServer           *grpc.Server
	configAsyncServer    *servers.ConfigAsyncServer
	appConfigAsyncServer *servers.AppConfigAsyncServer
	shutdownProcesses    []func()
	serfAgent            *services.SerfAgent
	clusterJoinListener  *services.ClusterJoinListener
	bootcHandler         *bootc.Handler
}

func NewAppWithConfig(config *configs.Config) (*app, error) {
	if config == nil {
		return nil, errors.New("config is nil")
	}
	return &app{
		config:            config,
		shutdownProcesses: make([]func(), 0),
	}, nil
}

func (a *app) init() {
	natsConn, err := NewNatsConn(a.config.NatsAddress())
	if err != nil {
		log.Fatalln(err)
	}
	a.shutdownProcesses = append(a.shutdownProcesses, func() {
		log.Println("closing nats conn")
		natsConn.Close()
	})

	nodeIdStore, err := store.NewNodeIdFSStore(a.config.NodeIdDirPath(), a.config.NodeIdFileName())
	if err != nil {
		log.Fatalln(err)
	}

	registrationClient, err := magnetarapi.NewRegistrationAsyncClient(a.config.NatsAddress())
	if err != nil {
		log.Fatalln(err)
	}

	capabilities := discoverCapabilities()
	osStatus := buildOsStatus()
	log.Println("node capabilities: ", capabilities)

	registrationService := services.NewRegistrationService(registrationClient, nodeIdStore)
	if !registrationService.Registered() {
		err := registrationService.Register(a.config.MaxRegistrationRetries(), time.Duration(a.config.RegistrationReqTimeoutMilliseconds())*time.Millisecond, a.config.SerfBindAddress(), capabilities, osStatus)
		if err != nil {
			log.Fatalln(err)
		}
	}

	nodeId, err := nodeIdStore.Get()
	if err != nil {
		log.Fatalln(err)
	}
	log.Println("NODE ID: " + nodeId.Value)

	nodeUpdateClient, err := magnetarapi.NewNodeUpdateAsyncClient(a.config.NatsAddress())
	if err != nil {
		log.Fatalln(err)
	}
	// With the bootc module running, its poller reports the OS phase; the
	// booted digest alone says nothing about greenboot's verdict, so a
	// "healthy" here would be premature right after a switch-reboot.
	syncStatus := osStatus
	if slices.Contains(capabilities, magnetarapi.CapabilityOsUpdate) {
		syncStatus = nil
	}
	// A node that is unknown to Magnetar re-registers asserting the id it
	// already has: the record is recreated with the same UUID instead of
	// duplicating the node under a fresh one.
	restartOnce := &sync.Once{}
	restartForReRegister := func() {
		restartOnce.Do(func() {
			// re-registration happens at startup; the service manager restarts us
			log.Println("node unknown to Magnetar, restarting to re-register")
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		})
	}
	if !syncNodeState(nodeUpdateClient, nodeId.Value, capabilities, syncStatus) {
		log.Println("node not found in Magnetar, re-registering with the same node id")
		err = registrationService.Register(a.config.MaxRegistrationRetries(), time.Duration(a.config.RegistrationReqTimeoutMilliseconds())*time.Millisecond, a.config.SerfBindAddress(), capabilities, osStatus)
		if err != nil {
			log.Fatalln(err)
		}
		nodeId, err = nodeIdStore.Get()
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("re-registered with NODE ID: " + nodeId.Value)
	}

	// Heartbeats run for every node, independent of the modules below: they
	// feed Magnetar's liveness view/GC and tell nodes without the bootc
	// module that the control plane lost them.
	heartbeatClient, err := magnetarapi.NewNodeHeartbeatAsyncClient(a.config.NatsAddress())
	if err != nil {
		log.Fatalln(err)
	}
	heartbeat := services.NewHeartbeatService(heartbeatClient, nodeId.Value, a.config.HeartbeatInterval())
	heartbeat.SetUnknownNodeHandler(restartForReRegister)
	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	a.shutdownProcesses = append(a.shutdownProcesses, heartbeatCancel)
	go heartbeat.Run(heartbeatCtx)

	if slices.Contains(capabilities, magnetarapi.CapabilityOsUpdate) {
		log.Println("starting bootc module")
		ctx, cancel := context.WithCancel(context.Background())
		a.shutdownProcesses = append(a.shutdownProcesses, cancel)

		poller := bootc.NewPoller(a.config.OsPollInterval(), bootc.StateDir(), nil)
		poller.SetImageRegistry(a.config.ImageRegistry())
		reporter := bootc.NewReporter(nodeUpdateClient, nodeId.Value, poller.Attempts())
		reporter.SetUnknownNodeHandler(restartForReRegister)
		poller.SetStatusHandler(reporter.Report)
		osClient, err := magnetarapi.NewOsAsyncClient(a.config.NatsAddress())
		if err != nil {
			log.Fatalln(err)
		}
		poller.SetDesiredSource(osClient, nodeId.Value)
		handler := bootc.NewHandler(natsConn, nodeId.Value, poller.Desired(), poller.Attempts())
		err = handler.Serve()
		if err != nil {
			log.Fatalln(err)
		}
		a.bootcHandler = handler

		go poller.Run(ctx)
	}

	if slices.Contains(capabilities, magnetarapi.CapabilityRunVms) {
		log.Println("starting vm reconciler module")
		ctx, cancel := context.WithCancel(context.Background())
		a.shutdownProcesses = append(a.shutdownProcesses, cancel)

		reconciler, err := vm.NewReconciler(vm.ReconcilerConfig{
			HostId:          nodeId.Value,
			Interval:        a.config.VmPollInterval(),
			CacheDir:        a.config.VmCacheDir(),
			StateDir:        a.config.VmStateDir(),
			IPBase:          a.config.VmIpBase(),
			IPOffset:        a.config.VmIpOffset(),
			NATSAddress:     a.config.NatsAddress(),
			DiskRegistryURL: a.config.DiskRegistryUrl(),
			ManagerConfig: vm.ManagerConfig{
				Network:   a.config.VmNetwork(),
				OSVariant: a.config.VmOsVariant(),
				// bootc-image-builder images are UEFI-only
				UEFI: true,
			},
		})
		if err != nil {
			log.Fatalln(err)
		}

		go reconciler.Run(ctx)
	}

	configStore, err := store.NewConfigInMemStore()
	if err != nil {
		log.Fatalln(err)
	}

	agent, err := services.NewSerfAgent(a.config, natsConn, nodeId.Value, configStore)
	if err != nil {
		log.Fatalln(err)
	}
	a.serfAgent = agent

	a.clusterJoinListener = services.NewClusterJoinListener(natsConn, a.serfAgent, nodeId.Value, nodeIdStore)

	configClient, err := kuiperapi.NewKuiperAsyncClient(a.config.NatsAddress(), nodeId.Value)
	if err != nil {
		log.Fatalln(err)
	}
	configAsyncServer, err := servers.NewConfigAsyncServer(configClient, configStore, agent, nodeId.Value)
	if err != nil {
		log.Fatalln(err)
	}
	a.configAsyncServer = configAsyncServer

	meridianClient, err := meridianapi.NewMeridianAsyncClient(a.config.NatsAddress(), nodeId.Value)
	if err != nil {
		log.Fatalln(err)
	}
	appConfigAsyncServer, err := servers.NewAppConfigAsyncServer(meridianClient, agent, nodeId.Value)
	if err != nil {
		log.Fatalln(err)
	}
	a.appConfigAsyncServer = appConfigAsyncServer

	configGrpcServer, err := servers.NewStarConfigServer(configStore)
	if err != nil {
		log.Fatalln(err)
	}

	s := grpc.NewServer()
	api.RegisterStarConfigServer(s, configGrpcServer)
	reflection.Register(s)
	a.grpcServer = s
}

func (a *app) startSerfAgent() error {
	// todo: join on signal
	// err := a.serfAgent.Join(true)
	// if err != nil {
	// 	return err
	// }
	// a.serfAgent.RunMock()
	//a.serfAgent.RunMock2()
	a.serfAgent.Wg.Add(1)
	go a.serfAgent.Listen()
	// go a.serfAgent.ListenNATS()
	return nil
}

func (a *app) startConfigAsyncServer() error {
	a.configAsyncServer.Serve()
	a.appConfigAsyncServer.Serve()
	return nil
}

func (a *app) startGrpcServer() error {
	lis, err := net.Listen("tcp", a.config.GrpcServerAddress())
	if err != nil {
		return err
	}
	go func() {
		log.Printf("server listening at %v", lis.Addr())
		if err := a.grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	return nil
}

func (a *app) Start() error {
	a.init()

	err := a.startConfigAsyncServer()
	if err != nil {
		return err
	}
	err = a.startGrpcServer()
	if err != nil {
		return err
	}
	err = a.startSerfAgent()
	if err != nil {
		return err
	}
	a.clusterJoinListener.Listen()
	return nil
}

func (a *app) GracefulStop() {
	go a.configAsyncServer.GracefulStop()
	a.grpcServer.GracefulStop()
	a.serfAgent.Leave()
	if a.bootcHandler != nil {
		a.bootcHandler.GracefulStop()
	}
	for _, shutdownProcess := range a.shutdownProcesses {
		shutdownProcess()
	}
}
