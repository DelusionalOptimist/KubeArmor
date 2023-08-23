package state

import (
	"context"
	"fmt"
	"time"

	kl "github.com/kubearmor/KubeArmor/KubeArmor/common"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	pb "github.com/kubearmor/KubeArmor/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

type StateAgent struct {
	StateEvents chan *pb.StateEvent

	StateAgentAddr string

	Running bool
	PodEntity string

	SAClient *StateAgentClient
}

type StateAgentClient struct {
	Conn *grpc.ClientConn
	Client pb.StateAgent_StateAgentClient
}

func NewStateAgent(addr string) *StateAgent {
	host, port, err := ParseURL(addr)
	if err != nil {
		kg.Err("Error while parsing State Agent URL")
		return nil
	}

	var podEntity string
	if ok := kl.IsK8sEnv(); ok {
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

	sm := &StateAgent{
		StateAgentAddr: fmt.Sprintf("%s:%s", host, port),
		StateEvents: make(chan *pb.StateEvent),
		Running: true,
		PodEntity: podEntity,
	}

	return sm
}

func (sm *StateAgent) RunStateAgent() {
	kg.Print("Trying to connect with State Agent Server")

	for sm.Running {
		// connect with state agent service
		conn, client, err := connectWithStateAgentService(sm.StateAgentAddr)
		if err != nil {
			kg.Debugf("Failed to connect with StateAgent at %s: %s", sm.StateAgentAddr, err.Error())
			// TODO: backoff algorithm
			time.Sleep(5 * time.Second)
			continue
		}

		sm.SAClient = &StateAgentClient{
			Conn: conn,
			Client: client,
		}

		kg.Printf("Connected with State Agent for reporting state")

		sm.ReportState()
	}
}

func (sm *StateAgent) ReportState() {
	client := sm.SAClient.Client
	closeChan := make(chan struct{})

	defer sm.SAClient.Conn.Close()

	go func(){
		_, err := client.Recv()
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.OK:
				default:
					kg.Warnf("Error while receiving reply from State Agent. %s", err)
				}
			}
		}
		closeChan <- struct{}{}
	}()

	// send cached "added" events
	for _, event := range StateEventCache {
		if s, ok := status.FromError(client.Send(event)); ok {
			switch s.Code() {
			case codes.OK:
			default:
				kg.Warnf("Failed to send cached state event.", s.Err().Error())
			}
		}
	}

	for {
		select {
		case <-closeChan:
			kg.Printf("Closing connection with State Agent")
			return
		case event := <- sm.StateEvents:
			if s, ok := status.FromError(client.Send(event)); ok {
				switch s.Code() {
				case codes.OK:
					continue
				default:
					kg.Warnf("Failed to send state event.", s.Err().Error())
					// TODO: backoff then retry
					return
				}
			}
		}
	}
}

func connectWithStateAgentService(addr string) (*grpc.ClientConn, pb.StateAgent_StateAgentClient, error) {
	kacp := keepalive.ClientParameters{
		Time:                1 * time.Second,
		Timeout:             5 * time.Second,
		PermitWithoutStream: true,
	}

	conn, err := grpc.Dial(addr, grpc.WithInsecure(), grpc.WithKeepaliveParams(kacp))
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	client := pb.NewStateAgentClient(conn)
	if client == nil {
		err = fmt.Errorf("Failed to create StateAgent client")
		return nil, nil, err
	}

	saClient, err := client.StateAgent(context.Background())
	if err != nil {
		err := fmt.Errorf("Failed to create StateAgent StateReportClient: %s", err.Error())
		return nil, nil, err
	}

	// TODO: healthchecks
	// hc := pb.NewHealthClient(conn)

	return conn, saClient, nil
}
