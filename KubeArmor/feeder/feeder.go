// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Authors of KubeArmor

// Package feeder is responsible for sanitizing and relaying telemetry and alerts data to connected clients
package feeder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	kl "github.com/kubearmor/KubeArmor/KubeArmor/common"
	cfg "github.com/kubearmor/KubeArmor/KubeArmor/config"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	tp "github.com/kubearmor/KubeArmor/KubeArmor/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	pb "github.com/kubearmor/KubeArmor/protobuf"
)

// ========== //
// == gRPC == //
// ========== //

// EventStruct Structure
type EventStruct[T any] struct {
	Filter    string
	Broadcast chan *T
}

type EventStructs struct {
	MsgStructs map[string]EventStruct[pb.Message]
	MsgLock    sync.RWMutex

	AlertStructs map[string]EventStruct[pb.Alert]
	AlertLock    sync.RWMutex

	LogStructs map[string]EventStruct[pb.Log]
	LogLock    sync.RWMutex
}

// AddMsgStruct Function
func (es *EventStructs) AddMsgStruct(filter string, queueSize int) (string, chan *pb.Message) {
	es.MsgLock.Lock()
	defer es.MsgLock.Unlock()

	uid := uuid.Must(uuid.NewRandom()).String()
	conn := make(chan *pb.Message, queueSize)

	msgStruct := EventStruct[pb.Message]{
		Filter:    filter,
		Broadcast: conn,
	}

	es.MsgStructs[uid] = msgStruct

	return uid, conn
}

// RemoveMsgStruct Function
func (es *EventStructs) RemoveMsgStruct(uid string) {
	es.MsgLock.Lock()
	defer es.MsgLock.Unlock()

	delete(es.MsgStructs, uid)
}

// AddAlertStruct Function
func (es *EventStructs) AddAlertStruct(filter string, queueSize int) (string, chan *pb.Alert) {
	es.AlertLock.Lock()
	defer es.AlertLock.Unlock()

	uid := uuid.Must(uuid.NewRandom()).String()
	conn := make(chan *pb.Alert, queueSize)

	alertStruct := EventStruct[pb.Alert]{
		Filter:    filter,
		Broadcast: conn,
	}

	es.AlertStructs[uid] = alertStruct

	return uid, conn
}

// removeAlertStruct Function
func (es *EventStructs) RemoveAlertStruct(uid string) {
	es.AlertLock.Lock()
	defer es.AlertLock.Unlock()

	delete(es.AlertStructs, uid)
}

// addLogStruct Function
func (es *EventStructs) AddLogStruct(filter string, queueSize int) (string, chan *pb.Log) {
	es.LogLock.Lock()
	defer es.LogLock.Unlock()

	uid := uuid.Must(uuid.NewRandom()).String()
	conn := make(chan *pb.Log, queueSize)

	logStruct := EventStruct[pb.Log]{
		Filter:    filter,
		Broadcast: conn,
	}

	es.LogStructs[uid] = logStruct

	return uid, conn
}

// removeLogStruct Function
func (es *EventStructs) RemoveLogStruct(uid string) {
	es.LogLock.Lock()
	defer es.LogLock.Unlock()

	delete(es.LogStructs, uid)
}

// ============ //
// == Feeder == //
// ============ //
type FeederInterface interface {
	// Methods

	// How does the feeder pushes logs and messages
	PushLog(tp.Log)
	PushMessage(string, string)

	// How does this feeder match log with policy
	UpdateMatchedPolicy(tp.Log)

	// How this feeder serves log feeds
	ServeLogFeeds()
}

type BaseFeeder struct {
	// node
	Node     *tp.Node
	NodeLock **sync.RWMutex

	// wait group
	WgServer sync.WaitGroup

	// output
	Output  string
	LogFile *os.File

	// Activated Enforcer
	Enforcer string

	// Msg, log and alert connection stores
	EventStructs *EventStructs

	/*
	MsgStructs map[string]EventStruct[pb.Message]
	MsgLock    sync.RWMutex

	AlertStructs map[string]EventStruct[pb.Alert]
	AlertLock    sync.RWMutex

	LogStructs map[string]EventStruct[pb.Log]
	LogLock    sync.RWMutex
	*/

	// True if feeder and its workers are working
	Running bool

	// LogServer //

	// port
	Port string

	// gRPC listener
	Listener net.Listener

	// log server
	LogServer *grpc.Server

	// ReverseLogServer //

	// URL of RelayServer
	RelayServerURL string

	// log server with connection initalized by KubeArmor
	ReverseLogServer *ReverseLogService

	Context context.Context
	Cancel context.CancelFunc
}

// Feeder Structure
type Feeder struct {
	BaseFeeder

	// KubeArmor feeder //

	// namespace name + endpoint name / host name -> corresponding security policies
	SecurityPolicies     map[string]tp.MatchPolicies
	SecurityPoliciesLock *sync.RWMutex

	// DefaultPosture (namespace -> postures)
	DefaultPostures     map[string]tp.DefaultPosture
	DefaultPosturesLock *sync.Mutex
}

// NewFeeder Function
func NewFeeder(node *tp.Node, nodeLock **sync.RWMutex) *Feeder {
	fd := &Feeder{}

	// base feeder //

	// node
	fd.Node = node
	fd.NodeLock = nodeLock

	// set wait group
	fd.WgServer = sync.WaitGroup{}

	// output
	fd.Output = cfg.GlobalCfg.LogPath

	// output mode
	if fd.Output != "stdout" && fd.Output != "none" {
		// #nosec
		logFile, err := os.OpenFile(filepath.Clean(fd.Output), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			kg.Errf("Failed to open %s", fd.Output)
			return nil
		}
		fd.LogFile = logFile
	}

	// default enforcer
	fd.Enforcer = "eBPF Monitor"

	// initialize msg structs
	fd.EventStructs = &EventStructs{
		MsgStructs: make(map[string]EventStruct[pb.Message]),
		MsgLock:    sync.RWMutex{},

		// initialize alert structs
		AlertStructs: make(map[string]EventStruct[pb.Alert]),
		AlertLock:    sync.RWMutex{},

		// initialize log structs
		LogStructs: make(map[string]EventStruct[pb.Log]),
		LogLock:    sync.RWMutex{},
	}

	/*
	fd.MsgStructs = make(map[string]EventStruct[pb.Message])
	fd.MsgLock = sync.RWMutex{}

	// initialize alert structs
	fd.AlertStructs = make(map[string]EventStruct[pb.Alert])
	fd.AlertLock = sync.RWMutex{}

	// initialize log structs
	fd.LogStructs = make(map[string]EventStruct[pb.Log])
	fd.LogLock = sync.RWMutex{}
	*/

	fd.Running = true

	// LogServer //

	// gRPC configuration
	fd.Port = fmt.Sprintf(":%s", cfg.GlobalCfg.GRPC)

	// listen to gRPC port
	listener, err := net.Listen("tcp", fd.Port)
	if err != nil {
		kg.Errf("Failed to listen a port (%s, %s)", fd.Port, err.Error())
		return nil
	}
	fd.Listener = listener

	if cfg.GlobalCfg.GRPC == "0" {
		pidFile, err := os.Create(cfg.PIDFilePath)
		if err != nil {
			kg.Errf("Failed to create file %s", cfg.PIDFilePath)
			return nil
		}

		defer func() {
			err := pidFile.Close()
			if err != nil {
				kg.Errf("Failed to close file %s", cfg.PIDFilePath)
			}
		}()

		port := fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
		fd.Port = fmt.Sprintf(":%s", port)

		_, err = pidFile.WriteString(port)
		if err != nil {
			kg.Errf("Failed to write file %s", cfg.PIDFilePath)
			return nil
		}
	}

	// create a log server

	logService := &LogService{
		/*
		BaseLogServer: BaseLogServer {
			MsgStructs: fd.MsgStructs,
			MsgLock: &fd.MsgLock,

			AlertStructs: fd.AlertStructs,
			AlertLock: &fd.AlertLock,

			LogStructs: fd.LogStructs,
			LogLock: &fd.LogLock,
		},
		*/
		QueueSize:    1000,
		Running:      &fd.Running,
		EventStructs: fd.EventStructs,
	}

	fd.LogServer = grpc.NewServer()

	pb.RegisterLogServiceServer(fd.LogServer, logService)
	grpc_health_v1.RegisterHealthServer(fd.LogServer, health.NewServer())

	// ReverseLogServer //

	host, port, err := kl.ParseURL(cfg.GlobalCfg.RelayServerURL)
	if err != nil {
		kg.Errf("Failed to parse Relay Server URL: %s", err.Error())
		return nil
	}
	fd.RelayServerURL = fmt.Sprintf("%s:%s", host, port)

	fd.Context, fd.Cancel = context.WithCancel(context.Background())

	// Feeder //

	// initialize security policies
	fd.SecurityPolicies = map[string]tp.MatchPolicies{}
	fd.SecurityPoliciesLock = new(sync.RWMutex)

	// initialize default postures
	fd.DefaultPostures = map[string]tp.DefaultPosture{}
	fd.DefaultPosturesLock = new(sync.Mutex)

	return fd
}

// DestroyFeeder Function
func (fd *BaseFeeder) DestroyFeeder() error {
	// stop gRPC service
	fd.Running = false

	// wait for a while
	time.Sleep(time.Second * 1)

	fd.Cancel()

	// close listener
	if fd.Listener != nil {
		if err := fd.Listener.Close(); err != nil {
			kg.Err(err.Error())
		}
		fd.Listener = nil
	}

	// close LogFile
	if fd.LogFile != nil {
		if err := fd.LogFile.Close(); err != nil {
			kg.Err(err.Error())
		}
		fd.LogFile = nil
	}

	// close reverse log server
	if fd.ReverseLogServer != nil {
		if fd.ReverseLogServer.Conn != nil {
			err := fd.ReverseLogServer.Conn.Close()
			if err != nil {
				return err
			}
		}
	}

	// wait for other routines
	fd.WgServer.Wait()

	return nil
}

// StrToFile Function
func (fd *Feeder) StrToFile(str string) {
	if fd.LogFile != nil {
		// add the newline at the end of the string
		str = str + "\n"

		// write the string into the file
		w := bufio.NewWriter(fd.LogFile)
		if _, err := w.WriteString(str); err != nil {
			kg.Err(err.Error())
		}

		// flush the file buffer
		if err := w.Flush(); err != nil {
			kg.Err(err.Error())
		}
	}
}

// ============== //
// == Messages == //
// ============== //

// Print Function
func (fd *Feeder) Print(message string) {
	fd.PushMessage("INFO", message)
	kg.Print(message)
}

// Printf Function
func (fd *Feeder) Printf(message string, args ...interface{}) {
	str := fmt.Sprintf(message, args...)
	fd.PushMessage("INFO", str)
	kg.Print(str)
}

// Debug Function
func (fd *Feeder) Debug(message string) {
	fd.PushMessage("DEBUG", message)
	kg.Debug(message)
}

// Debugf Function
func (fd *Feeder) Debugf(message string, args ...interface{}) {
	str := fmt.Sprintf(message, args...)
	fd.PushMessage("DEBUG", str)
	kg.Debug(str)
}

// Err Function
func (fd *Feeder) Err(message string) {
	fd.PushMessage("ERROR", message)
	kg.Err(message)
}

// Errf Function
func (fd *Feeder) Errf(message string, args ...interface{}) {
	str := fmt.Sprintf(message, args...)
	fd.PushMessage("ERROR", str)
	kg.Err(str)
}

// Warn Function
func (fd *Feeder) Warn(message string) {
	fd.PushMessage("WARN", message)
	kg.Warn(message)
}

// Warnf Function
func (fd *Feeder) Warnf(message string, args ...interface{}) {
	str := fmt.Sprintf(message, args...)
	fd.PushMessage("WARN", str)
	kg.Warnf(str)
}

// ===================== //
// == Enforcer Update == //
// ===================== //

// UpdateEnforcer Function
func (fd *Feeder) UpdateEnforcer(enforcer string) {
	fd.Enforcer = enforcer
}

// =============== //
// == Log Feeds == //
// =============== //

// ServeLogFeeds Function
func (fd *BaseFeeder) ServeLogFeeds() {
	fd.WgServer.Add(1)
	defer fd.WgServer.Done()

	// feed logs
	if err := fd.LogServer.Serve(fd.Listener); err != nil {
		kg.Print("Terminated the gRPC service")
	}
}

func (fd *BaseFeeder) ServeReverseLogFeeds() {
	fd.WgServer.Add(1)
	defer fd.WgServer.Done()

	for fd.Running {
		fd.ReverseLogServer = fd.ConnectWithRelay()
		if fd.ReverseLogServer == nil {
			return
		}

		kg.Printf("Connected with RelayServer for pushing logs in reverse (%s)", fd.RelayServerURL)

		fd.ReverseLogServer.Wg.Add(1)
		go fd.ReverseLogServer.WatchLogs()

		fd.ReverseLogServer.Wg.Add(1)
		go fd.ReverseLogServer.WatchAlerts()

		fd.ReverseLogServer.Wg.Add(1)
		go fd.ReverseLogServer.WatchMessages()

		time.Sleep(time.Second * 1)

		// wait for other routines to terminate before creating a new connection
		fd.ReverseLogServer.Wg.Wait()

		// destroy client
		if err := fd.ReverseLogServer.Conn.Close(); err != nil {
			kg.Warnf("Failed to delete ReverseLogClient: %s", err.Error())
		}
		kg.Printf("Closed ReverseLogClient for %s", fd.RelayServerURL)

		fd.ReverseLogServer = nil
	}

	kg.Print("Stopped Pushing events on gRPC ReverseLogService")
}

// PushMessage Function
func (fd *Feeder) PushMessage(level, message string) {
	pbMsg := pb.Message{}

	timestamp, updatedTime := kl.GetDateTimeNow()

	pbMsg.Timestamp = timestamp
	pbMsg.UpdatedTime = updatedTime

	//pbMsg.ClusterName = cfg.GlobalCfg.Cluster
	pbMsg.ClusterName = fd.Node.ClusterName

	pbMsg.HostName = cfg.GlobalCfg.Host
	pbMsg.HostIP = fd.Node.NodeIP

	pbMsg.Type = "Message"

	pbMsg.Level = level
	pbMsg.Message = message

	// broadcast to all logserver and reverselogserver receivers
	fd.EventStructs.MsgLock.Lock()
	defer fd.EventStructs.MsgLock.Unlock()
	counter := 0
	lenMsg := len(fd.EventStructs.MsgStructs)
	for uid := range fd.EventStructs.MsgStructs {
		select {
		case fd.EventStructs.MsgStructs[uid].Broadcast <- &pbMsg:
		default:
			counter++
			if counter == lenMsg {
				//Default on the last uid in Messagestruct means the msg isnt pushed into Broadcast
				kg.Printf("msg channel busy, msg dropped")
			}

		}
	}
}

// PushLog Function
func (fd *Feeder) PushLog(log tp.Log) {
	log = fd.UpdateMatchedPolicy(log)

	if log.Source == "" {
		return
	}

	// set hostname
	log.HostName = cfg.GlobalCfg.Host

	// remove MergedDir
	log.MergedDir = ""

	// remove flags
	log.PolicyEnabled = 0
	log.ProcessVisibilityEnabled = false
	log.FileVisibilityEnabled = false
	log.NetworkVisibilityEnabled = false
	log.CapabilitiesVisibilityEnabled = false

	// standard output / file output
	if fd.Output == "stdout" {
		arr, _ := json.Marshal(log)
		fmt.Println(string(arr))
	} else if fd.Output != "none" {
		arr, _ := json.Marshal(log)
		fd.StrToFile(string(arr))
	}

	// gRPC output
	if log.Type == "MatchedPolicy" || log.Type == "MatchedHostPolicy" {
		pbAlert := pb.Alert{}

		pbAlert.Timestamp = log.Timestamp
		pbAlert.UpdatedTime = log.UpdatedTime

		pbAlert.ClusterName = fd.Node.ClusterName
		pbAlert.HostName = fd.Node.NodeName

		pbAlert.NamespaceName = log.NamespaceName

		var owner *pb.Podowner
		if log.Owner != nil && (log.Owner.Ref != "" || log.Owner.Name != "" || log.Owner.Namespace != "") {
			owner = &pb.Podowner{
				Ref:       log.Owner.Ref,
				Name:      log.Owner.Name,
				Namespace: log.Owner.Namespace,
			}
		}

		if pbAlert.Owner == nil && owner != nil {
			pbAlert.Owner = owner
		}

		pbAlert.PodName = log.PodName
		pbAlert.Labels = log.Labels

		pbAlert.ContainerID = log.ContainerID
		pbAlert.ContainerName = log.ContainerName
		pbAlert.ContainerImage = log.ContainerImage

		pbAlert.HostPPID = log.HostPPID
		pbAlert.HostPID = log.HostPID

		pbAlert.PPID = log.PPID
		pbAlert.PID = log.PID
		pbAlert.UID = log.UID

		pbAlert.ParentProcessName = log.ParentProcessName
		pbAlert.ProcessName = log.ProcessName

		if len(log.Enforcer) > 0 {
			pbAlert.Enforcer = log.Enforcer
		}

		if len(log.PolicyName) > 0 {
			pbAlert.PolicyName = log.PolicyName
		}

		if len(log.Severity) > 0 {
			pbAlert.Severity = log.Severity
		}

		if len(log.Tags) > 0 {
			pbAlert.Tags = log.Tags
			pbAlert.ATags = strings.Split(log.Tags, ",")
		}

		if len(log.Message) > 0 {
			pbAlert.Message = log.Message
		}

		pbAlert.Type = log.Type
		pbAlert.Source = log.Source
		pbAlert.Operation = log.Operation
		pbAlert.Resource = strings.ToValidUTF8(log.Resource, "")

		if len(log.Data) > 0 {
			pbAlert.Data = log.Data
		}

		if len(log.Action) > 0 {
			pbAlert.Action = log.Action
		}

		pbAlert.Result = log.Result

		fd.EventStructs.AlertLock.Lock()
		defer fd.EventStructs.AlertLock.Unlock()
		counter := 0
		lenAlert := len(fd.EventStructs.AlertStructs)

		for uid := range fd.EventStructs.AlertStructs {
			select {
			case fd.EventStructs.AlertStructs[uid].Broadcast <- &pbAlert:
			default:
				counter++
				if counter == lenAlert {
					//Default on the last uid in Alterstruct means the Alert isnt pushed into Broadcast
					kg.Printf("log channel busy, alert dropped.")
				}

			}
		}
	} else { // ContainerLog || HostLog
		pbLog := pb.Log{}

		pbLog.Timestamp = log.Timestamp
		pbLog.UpdatedTime = log.UpdatedTime

		pbLog.ClusterName = fd.Node.ClusterName
		pbLog.HostName = fd.Node.NodeName

		pbLog.NamespaceName = log.NamespaceName

		var owner *pb.Podowner
		if log.Owner != nil && (log.Owner.Ref != "" || log.Owner.Name != "" || log.Owner.Namespace != "") {
			owner = &pb.Podowner{
				Ref:       log.Owner.Ref,
				Name:      log.Owner.Name,
				Namespace: log.Owner.Namespace,
			}
		}

		if pbLog.Owner == nil && owner != nil {
			pbLog.Owner = owner
		}

		pbLog.PodName = log.PodName
		pbLog.Labels = log.Labels

		pbLog.ContainerID = log.ContainerID
		pbLog.ContainerName = log.ContainerName
		pbLog.ContainerImage = log.ContainerImage

		pbLog.HostPPID = log.HostPPID
		pbLog.HostPID = log.HostPID

		pbLog.PPID = log.PPID
		pbLog.PID = log.PID
		pbLog.UID = log.UID

		pbLog.ParentProcessName = log.ParentProcessName
		pbLog.ProcessName = log.ProcessName

		pbLog.Type = log.Type
		pbLog.Source = log.Source
		pbLog.Operation = log.Operation
		pbLog.Resource = strings.ToValidUTF8(log.Resource, "")

		if len(log.Data) > 0 {
			pbLog.Data = log.Data
		}

		pbLog.Result = log.Result

		fd.EventStructs.LogLock.Lock()
		defer fd.EventStructs.LogLock.Unlock()
		counter := 0
		lenlog := len(fd.EventStructs.LogStructs)
		for uid := range fd.EventStructs.LogStructs {
			select {
			case fd.EventStructs.LogStructs[uid].Broadcast <- &pbLog:
			default:
				counter++
				if counter == lenlog {
					//Default on the last uid in Logstuct means the log isnt pushed into Broadcase
					kg.Printf("log channel busy, log dropped.")
				}
			}
		}
	}
}
