// SPDX-License-Identifier: Apache-2.0
// Copyright 2023 Authors of KubeArmor

package state

import (
	"encoding/json"
	"time"

	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	"github.com/kubearmor/KubeArmor/KubeArmor/types"
	tp "github.com/kubearmor/KubeArmor/KubeArmor/types"
	pb "github.com/kubearmor/KubeArmor/protobuf"
)

// pushes (container + pod) & workload event
func (sa *StateAgent) PushContainerEvent(container tp.Container, event string) {
	if container.ContainerID == "" {
		kg.Debug("Error while pushing container event. Missing data.")
		return
	}

	// create ns first
	namespace := container.NamespaceName
	sa.KubeArmorNamespacesLock.Lock()
	if event == EventAdded {

		// create this kubearmor ns if it doesn't exist
		// currently only "container_namespace" until we have config agent
		if _, ok := sa.KubeArmorNamespaces[namespace]; !ok {
			nsEvent := types.Namespace{
				Name: namespace,
				//Labels: "",
				KubearmorFilePosture: "audit",
				KubearmorNetworkPosture: "audit",
				LastUpdatedAt: container.LastUpdatedAt,

				ContainerCount: 1,
			}

			sa.PushNamespaceEvent(nsEvent, EventAdded)
		} else {
			// update the container count
			meta := sa.KubeArmorNamespaces[namespace]
			meta.ContainerCount++
			sa.KubeArmorNamespaces[namespace] = meta
		}

	} else if event == EventDeleted {

		if ns, ok := sa.KubeArmorNamespaces[namespace]; ok {
			ns.ContainerCount--
			sa.KubeArmorNamespaces[namespace] = ns

			// delete ns if no containers left in it
			if ns.ContainerCount == 0 {
				ns.LastUpdatedAt = time.Now().UTC().String()
				sa.PushNamespaceEvent(ns, EventDeleted)
				delete(sa.KubeArmorNamespaces, namespace)
			}
		}

	}
	sa.KubeArmorNamespacesLock.Unlock()

	containerBytes, err := json.Marshal(container)
	if err != nil {
		kg.Warnf("Error while trying to marshal container data. %s", err.Error())
		return
	}

	containerEvent := &pb.StateEvent{
		Kind:   KindContainer,
		Type:   event,
		Name:   container.ContainerName,
		Object: containerBytes,
	}

	// skip sending message as no state receiver is connected
	if sa.StateEvents == nil {
		return
	}

	select {
	case sa.StateEvents <- containerEvent:
	default:
		kg.Debugf("Failed to send container %s state event", event)
		return
	}

	return
}

func (sa *StateAgent) PushNodeEvent(node tp.Node, event string) {
	if node.NodeName == "" {
		kg.Warn("Received empty node event")
		return
	}

	nodeData, err := json.Marshal(node)
	if err != nil {
		kg.Warnf("Error while trying to marshal node data. %s", err.Error())
		return
	}

	nodeEvent := &pb.StateEvent{
		Kind:   KindNode,
		Type:   event,
		Name:   node.NodeName,
		Object: nodeData,
	}

	// skip sending message as no state receiver is connected
	if sa.StateEvents == nil {
		return
	}

	select {
	case sa.StateEvents <- nodeEvent:
	default:
		kg.Debugf("Failed to send node %s state event.", event)
		return
	}

	return
}

func (sa *StateAgent) PushNamespaceEvent(namespace tp.Namespace, event string) {
	nsBytes, err := json.Marshal(namespace)
	if err != nil {
		kg.Warnf("Failed to marshal ns event: %s", err.Error())
		return
	}
	//fmt.Println("NAMESPACE-JSON:", string(nsBytes))

	nsEvent := &pb.StateEvent{
		Kind:   KindNamespace,
		Type:   event,
		Name:   namespace.Name,
		Object: nsBytes,
	}

	// skip sending message as no state receiver is connected
	if sa.StateEvents == nil {
		return
	}

	select {
	case sa.StateEvents <- nsEvent:
	default:
		kg.Debugf("Failed to send namespace %s state event", event)
		return
	}
}
