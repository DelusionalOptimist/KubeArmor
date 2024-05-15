// SPDX-License-Identifier: Apache-2.0
// Copyright 2023 Authors of KubeArmor

package feeder

import (
	"context"
	"fmt"
	"sync"
	"time"

	kl "github.com/kubearmor/KubeArmor/KubeArmor/common"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	pb "github.com/kubearmor/KubeArmor/protobuf"
)

var (
	LogOnce sync.Once
	MsgOnce sync.Once
	AlertOnce sync.Once
)

// LogService struct holds the state of feeder's log server
type LogService struct {
	QueueSize    int
	EventStructs *EventStructs
	Running      *bool
}

// HealthCheck Function
// Deprecated: use the server created with google.golang.org/grpc/health/grpc_health_v1
func (ls *LogService) HealthCheck(ctx context.Context, nonce *pb.NonceMessage) (*pb.ReplyMessage, error) {
	replyMessage := pb.ReplyMessage{Retval: nonce.Nonce}
	return &replyMessage, nil
}

// WatchMessages Function
func (ls *LogService) WatchMessages(req *pb.RequestMessage, svr pb.LogService_WatchMessagesServer) error {
	if ls.Running == nil {
		return fmt.Errorf("Feeder is not running")
	}

	uid, conn := ls.EventStructs.AddMsgStruct(req.Filter, ls.QueueSize)
	kg.Printf("Added a new client (%s) for WatchMessages", uid)

	defer func() {
		close(conn)
		ls.EventStructs.RemoveMsgStruct(uid)
		kg.Printf("Deleted the client (%s) for WatchMessages", uid)
	}()

	newTimer := time.NewTimer(time.Second * 150)

	for *ls.Running {
		select {
		case <- newTimer.C:
			var exit = false
			MsgOnce.Do(func() {
				exit = true
			})
			if exit {
				fmt.Println("Msg stream just dying after 100 seconds")
				return fmt.Errorf("Msg stream just dying after 100 seconds")
			}
		case <-svr.Context().Done():
			return nil
		case resp := <-conn:
			if err := kl.HandleGRPCErrors(svr.Send(resp)); err != nil {
				kg.Warnf("Failed to send a message=[%+v] err=[%s]", resp, err.Error())
				return err
			}
		}
	}

	return nil
}

// WatchAlerts Function
func (ls *LogService) WatchAlerts(req *pb.RequestMessage, svr pb.LogService_WatchAlertsServer) error {
	if ls.Running == nil {
		return fmt.Errorf("Feeder is not running")
	}

	if req.Filter != "all" && req.Filter != "policy" {
		return nil
	}

	uid, conn := ls.EventStructs.AddAlertStruct(req.Filter, ls.QueueSize)
	kg.Printf("Added a new client (%s, %s) for WatchAlerts", uid, req.Filter)

	defer func() {
		close(conn)
		ls.EventStructs.RemoveAlertStruct(uid)
		kg.Printf("Deleted the client (%s) for WatchAlerts", uid)
	}()

	newTimer := time.NewTimer(time.Second * 125)

	for *ls.Running {
		select {
		case <- newTimer.C:
			var exit = false
			AlertOnce.Do(func() {
				exit = true
			})
			if exit {
				fmt.Println("Alert stream just dying after 100 seconds")
				return fmt.Errorf("Alert stream just dying after 100 seconds")
			}
		case <-svr.Context().Done():
			return nil
		case resp := <-conn:
			if err := kl.HandleGRPCErrors(svr.Send(resp)); err != nil {
				kg.Warnf("Failed to send an alert=[%+v] err=[%s]", resp, err.Error())
				return err
			}
		}
	}

	return nil
}

// WatchLogs Function
func (ls *LogService) WatchLogs(req *pb.RequestMessage, svr pb.LogService_WatchLogsServer) error {
	if ls.Running == nil {
		return fmt.Errorf("Feeder is not running")
	}

	if req.Filter != "all" && req.Filter != "system" {
		return nil
	}

	uid, conn := ls.EventStructs.AddLogStruct(req.Filter, ls.QueueSize)
	kg.Printf("Added a new client (%s, %s) for WatchLogs", uid, req.Filter)

	defer func() {
		close(conn)
		ls.EventStructs.RemoveLogStruct(uid)
		kg.Printf("Deleted the client (%s) for WatchLogs", uid)
	}()

	newTimer := time.NewTimer(time.Second * 100)

	for *ls.Running {
		select {
		case <- newTimer.C:
			var exit = false
			LogOnce.Do(func() {
				exit = true
			})
			if exit {
				fmt.Println("Log stream just dying after 100 seconds")
				return fmt.Errorf("Log stream just dying after 100 seconds")
			}
		case <-svr.Context().Done():
			return nil
		case resp := <-conn:
			if err := kl.HandleGRPCErrors(svr.Send(resp)); err != nil {
				kg.Warnf("Failed to send a log=[%+v] err=[%s]", resp, err.Error())
				return err
			}
		}
	}

	return nil
}
