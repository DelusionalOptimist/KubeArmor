package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kubearmor/KubeArmor/KubeArmor/common"
	kl "github.com/kubearmor/KubeArmor/KubeArmor/common"
	cfg "github.com/kubearmor/KubeArmor/KubeArmor/config"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	pb "github.com/kubearmor/KubeArmor/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

type StateAgent struct {
	StateEvents chan *pb.StateEvent

	StateAgentAddr string

	Running   bool
	PodEntity string

	SAClient *StateAgentClient

	StateEventCache     map[string]*pb.StateEvent
	StateEventCacheLock *sync.RWMutex

	KubeArmorNamespaces map[string][]string

	Wg *sync.WaitGroup
	Context context.Context
	Cancel context.CancelFunc
}

type StateAgentClient struct {
	Conn   *grpc.ClientConn
	WatchClient pb.StateAgent_WatchStateClient
	GetClient pb.StateAgent_GetStateClient
}

func NewStateAgent(addr string) *StateAgent {
	host, port, err := common.ParseURL(addr)
	if err != nil {
		kg.Err("Error while parsing State Agent URL")
		return nil
	}

	var podEntity string
	if ok := kl.IsK8sEnv(); ok && cfg.GlobalCfg.K8sEnv {
		// pod == K8s pod
		podEntity = "k8s"
	} else if ok := kl.IsECSEnv(); ok {
		// pod == ECS task
		// taskMeta, ok := os.LookupEnv("ECS_CONTAINER_METADATA_URI_V4")
		podEntity = "task"
	} else {
		// pod == Container
		podEntity = ""
	}

	context, cancel := context.WithCancel(context.Background())

	sa := &StateAgent{
		StateAgentAddr: fmt.Sprintf("%s:%s", host, port),
		StateEvents:    make(chan *pb.StateEvent, 1),

		Running:   true,
		PodEntity: podEntity,

		StateEventCache:     make(map[string]*pb.StateEvent),
		StateEventCacheLock: new(sync.RWMutex),

		KubeArmorNamespaces: make(map[string][]string),

		Wg: new(sync.WaitGroup),
		Context: context,
		Cancel: cancel,
	}

	return sa
}

func (sa *StateAgent) RunStateAgent() {
	var err error

	for sa.Running {
		// connect with state agent service
		sa.SAClient, err = sa.connectWithStateAgentService()
		if err != nil {
			kg.Debugf("Failed to connect with StateAgent at %s: %s", sa.StateAgentAddr, err.Error())
			continue
		}

		kg.Printf("Connected with State Agent Service for reporting state")

		sa.Wg.Add(1)
		go sa.WatchStateClient()

		sa.Wg.Add(1)
		go sa.GetStateClient()

		sa.Wg.Wait()

		if err := sa.SAClient.Conn.Close(); err != nil {
			kg.Warnf("Failed to close State Agent client: %s", err.Error())
		}
		kg.Printf("Closed State Agent client")

		sa.SAClient = nil
	}

	kg.Printf("Stop streaming state events")
}

// DestroyStateAgent
func (sa *StateAgent) DestroyStateAgent() error {
	sa.Cancel()
	sa.Running = false
	time.Sleep(1 * time.Second)

	if sa.SAClient != nil {
		if sa.SAClient.Conn != nil {
			err := sa.SAClient.Conn.Close()
			if err != nil {
				return err
			}
		}
	}

	// wait for terminations
	sa.Wg.Wait()

	return nil
}

// sends state events in a continuous stream
func (sa *StateAgent) WatchStateClient() {
	defer sa.Wg.Done()

	kg.Print("Streaming State Events with WatchState Client")
	defer kg.Print("Closed WatchState Client")

	client := sa.SAClient.WatchClient
	closeChan := make(chan struct{})

	defer kg.Printf("Closed")

	go func() {
		defer close(closeChan)
		_, err := client.Recv()
		if err := kl.HandleGRPCErrors(err); err != nil {
			kg.Warnf("Error while receiving reply from State Agent WatchClient. %s", err.Error())
		}
		closeChan <- struct{}{}
	}()

	// send cached "added" events
	go func() {
		for _, event := range sa.StateEventCache {
			err := client.Send(event)
			if grpcErr := kl.HandleGRPCErrors(err); grpcErr != nil {
				kg.Warnf("Failed to send cached state event.")
				return
			}

			// below approach is DRY but has chances of losing state events
			/*
			select {
			case sa.StateEvents <- event:
			default:
				kg.Warnf("Failed to send cached state event.")
			}
			*/
		}
	}()

	for sa.Running {
		select {
		case <-client.Context().Done():
			return
		case <-closeChan:
			kg.Printf("Closing connection with State Agent Client")
			return
		case event := <-sa.StateEvents:
			if err := kl.HandleGRPCErrors(client.Send(event)); err != nil {
				kg.Warnf("Failed to send state event.", err.Error())
				return
			}
		}
	}
}

// sends state event stream upon request
func (sa *StateAgent) GetStateClient() {
	defer sa.Wg.Done()

	kg.Print("Streaming State Events with GetState Client")
	defer kg.Print("Closed GetState Client")

	client := sa.SAClient.GetClient

	for sa.Running {
		select {
		// to avoid panics when the connection has been terminated
		case <-client.Context().Done():
			return
		default:
			_, err := client.Recv()
			if err := kl.HandleGRPCErrors(err); err != nil {
				kg.Warnf("Error while receiving request from GetState Client %s", err.Error())
				continue
			}

			stateEventList := make([]*pb.StateEvent, 1)
			for _, event := range sa.StateEventCache {
				stateEventList = append(stateEventList, event)
			}

			stateEvents := &pb.StateEvents{
				StateEvents: stateEventList,
			}

			err = client.Send(stateEvents)
			if err := kl.HandleGRPCErrors(err); err != nil {
				kg.Warnf("Failed to send State Events to GetState Client: ", err.Error())
				return
			}
		}
	}
}

func (sa *StateAgent) connectWithStateAgentService() (*StateAgentClient, error) {
	var (
		err error
		conn *grpc.ClientConn
		client pb.StateAgentClient
	)

	kacp := keepalive.ClientParameters{
		Time:                1 * time.Second,
		Timeout:             5 * time.Second,
		PermitWithoutStream: true,
	}

	for sa.Running {
		conn, err = grpc.DialContext(sa.Context, sa.StateAgentAddr, grpc.WithInsecure(), grpc.WithKeepaliveParams(kacp))
		if err != nil {
			time.Sleep(time.Second * 5)
			conn.Close()
			continue
		}

		client = pb.NewStateAgentClient(conn)
		if client == nil {
			time.Sleep(time.Second * 5)
			conn.Close()
			continue
		}

		healthClient := grpc_health_v1.NewHealthClient(conn)
		healthCheckRequest := &grpc_health_v1.HealthCheckRequest{
			Service: pb.StateAgent_ServiceDesc.ServiceName,
		}

		resp, err := healthClient.Check(sa.Context, healthCheckRequest)
		grpcErr := kl.HandleGRPCErrors(err)
		if grpcErr != nil {
			kg.Debugf("State Agent Service unhealthy. Error: %s", grpcErr.Error())
			conn.Close()
			time.Sleep(time.Second * 5)
			continue
		}

		switch resp.Status {
		case grpc_health_v1.HealthCheckResponse_SERVING:
			break
		case grpc_health_v1.HealthCheckResponse_NOT_SERVING:
			conn.Close()
			return nil, fmt.Errorf("State Agent server is not serving")
		default:
			kg.Debugf("State Agent Service unhealthy. Status: %s", resp.Status.String())
			conn.Close()
			time.Sleep(time.Second * 5)
			continue
		}

		break
	}

	watchClient, err := client.WatchState(sa.Context)
	if err != nil {
		err := fmt.Errorf("Failed to create StateAgent WatchClient: %s", err.Error())
		return nil, err
	}

	getClient, err := client.GetState(sa.Context)
	if err != nil {
		err := fmt.Errorf("Failed to create StateAgent GetClient: %s", err.Error())
		return nil, err
	}

	saClient := &StateAgentClient{
		Conn:   conn,
		WatchClient: watchClient,
		GetClient: getClient,
	}

	return saClient, nil
}
