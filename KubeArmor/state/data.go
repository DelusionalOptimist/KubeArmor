package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cfg "github.com/kubearmor/KubeArmor/KubeArmor/config"
	kg "github.com/kubearmor/KubeArmor/KubeArmor/log"
	tp "github.com/kubearmor/KubeArmor/KubeArmor/types"
	pb "github.com/kubearmor/KubeArmor/protobuf"
)

var (
	StateEventCache = make(map[string]*pb.StateEvent)
	//KubeArmorNamespaces = make(map[string][]string)
)

func (sa *StateAgent) PushContainerEvent(container tp.Container, event string) {
	if container.ContainerID == "" {
		kg.Debugf("Error while pushing container event. Incomplete data.")
		return
	}

	containerData, err := processContainerEvent(sa.PodEntity, container, event)
	if err != nil {
		kg.Debugf("Error while processing container data. %s", err.Error())
		return
	}

	if len(containerData) == 0 {
		kg.Debugf("Failed to process container data.")
		return
	}

	stateEvent := &pb.StateEvent{
		Kind:   "pod",
		Type:   event,
		Object: containerData,
	}

	//namespace := container.NamespaceName
	cacheKey := fmt.Sprintf("kubearmor-container-%.12s", container.ContainerID)
	if event == "added" {
		StateEventCache[cacheKey] = stateEvent
		/*
			// create this kubearmor ns if it doesn't exist
			if _, ok := KubeArmorNamespaces[namespace]; !ok {
				KubeArmorNamespaces[namespace] = []string{}
				KubeArmorNamespaces[namespace] = append(KubeArmorNamespaces[container.NamespaceName], container.ContainerID)
				go sa.PushNamespaceEvent(namespace, "added")
			}
		*/
	} else if event == "deleted" {
		delete(StateEventCache, cacheKey)
		/*
			// delete this container from kubearmor ns
			if containers, ok := KubeArmorNamespaces[namespace]; ok {
				containerDeleted := false
				for i, c := range containers {
					if c == container.ContainerID {
						newNSList := kl.RemoveStringElement(containers, i)
						KubeArmorNamespaces[namespace] = newNSList
						break
					}
				}

				// no containers left - namespace deleted
				if containerDeleted && len(KubeArmorNamespaces[namespace]) > 0 {
					go sa.PushNamespaceEvent(namespace, "deleted")
				}
			}
		*/
	}

	select {
	case sa.StateEvents <- stateEvent:
	default:
		kg.Warnf("Failed to send container state event")
	}

	return
}

func processContainerEvent(podE string, container tp.Container, event string) ([]byte, error) {
	// pod entity doesn't exist
	if podE == "" {

		// ContainerDetails
		containerD := &tp.ContainerDetails{
			ContainerName: container.ContainerName,
			Image:         container.ContainerImage,
			ContainerId:   container.ContainerID,
			Status:        container.Status,
			ProtocolPort:  container.ProtocolPort,
			NameOfService: container.ContainerName,
		}
		containerDetails := []*tp.ContainerDetails{containerD}

		/* TODO - why SIA uses only single port
		var protcolPort []string
		for n, port := range container.NetworkSettings.Ports {
			if len(port) > 0 {
				portInfo := strings.Split(string(n), "/")
				if len(portInfo) != 2 {
					continue
				}
				var k8sPort v1.ContainerPort
				for _, p := range port {
					intHPort, _ := strconv.ParseInt(p.HostPort, 10, 32)
					intCPort, _ := strconv.ParseInt(portInfo[0], 10, 32)
					k8sPort = v1.ContainerPort {
						Name: fmt.Sprintf("%s:%d", p.HostIP, portInfo[0]),
						HostPort: int32(intHPort),
						ContainerPort: int32(intCPort),
						Protocol: v1.Protocol(strings.ToUpper(portInfo[1])),
						HostIP: p.HostIP,
					}
				}

				ports = append(ports, k8sPort)
			}
		}
		*/

		var labels []*tp.Labels
		labelsSlice := strings.Split(container.Labels, ",")
		for _, label := range labelsSlice {
			key, value, ok := strings.Cut(label, "=")
			if !ok {
				continue
			}
			l := &tp.Labels{
				Key:   key,
				Value: value,
			}

			labels = append(labels, l)
		}

		podDetails := tp.PodDetails{
			// ClusterID
			NewPodName: fmt.Sprintf("%s-%.6s", container.ContainerName, container.ContainerID),
			//OldPodName: "",
			Namespace:       container.NamespaceName,
			NodeName:        cfg.GlobalCfg.Host,
			LastUpdatedTime: time.Now().UTC().String(),
			Container:       containerDetails,
			Labels:          labels,
			Operation:       event,
			PodIp:           container.ContainerIP,
			WorkloadName:    containerD.ContainerName,
			WorkloadType:    "ReplicaSet",
		}

		podBytes, err := json.Marshal(podDetails)
		if err != nil {
			return nil, err
		}

		return podBytes, nil
	}

	return []byte(""), nil
}

func (sa *StateAgent) PushNodeEvent(node tp.Node, event string) {
	if node.NodeName == "" {
		kg.Warnf("Received empty node event")
		return
	}

	var labels []*tp.Labels
	for key, val := range node.Annotations {
		l := &tp.Labels{
			Key:   key,
			Value: val,
		}
		labels = append(labels, l)
	}

	nodeDetails := tp.NodeDetails{
		NewNodeName:     node.NodeName,
		LastUpdatedTime: time.Now().UTC().String(),
		Labels:          labels,
		Operation:       event,
	}

	nodeData, err := json.Marshal(nodeDetails)
	if err != nil {
		kg.Warnf("Failed to marshal node event: %s", err)
		return
	}

	stateEvent := &pb.StateEvent{
		Kind:   "node",
		Type:   event,
		Object: nodeData,
	}

	cacheKey := fmt.Sprintf("kubearmor-node-%s", node.NodeName)
	if event == "added" {
		StateEventCache[cacheKey] = stateEvent
	} else if event == "deleted" {
		delete(StateEventCache, cacheKey)
	}

	select {
	case sa.StateEvents <- stateEvent:
	default:
		kg.Debugf("Failed to send node event.")
	}

	return
}

/*
// All native ways to get node data
func GetNodeData() types.Node {
	addr, ok := os.LookupEnv("STATE_AGENT_ADDRESS")
	if !ok {
		kg.Err("Set STATE_AGENT_ADDRESS to proceed")
		return types.Node{}
	}

	host, _, err := ParseURL(addr)
	if err != nil {
		kg.Err("Error while parsing State Agent URL")
		return types.Node{}
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		kg.Err(err.Error())
		return types.Node{}
	}

	// get private IP
	ip, err := netlink.RouteGet(net.ParseIP(addrs[1]))
	if err != nil {
		kg.Err(err.Error())
		return types.Node{}
	}

	nodeIP := ip[0].Src
	nodeName, err := os.Hostname()
	if err != nil {
		kg.Err(err.Error())
	}

	nodeArch := runtime.GOARCH
	nodeOS := runtime.GOOS

	// get
	// nodeKernelVersion

	return types.Node {
		NodeIP: nodeIP.String(),
		NodeName: nodeName,
		Architecture: nodeArch,
		OperatingSystem: nodeOS,
	}
}

/*
// Extreme amount of pod data
func processContainerEvent(podE string, containerObj interface{}, runtime string) ([]byte, error) {
	// pod env doesn't exist
	if podE == "" {
		switch runtime {
		case "docker":
			container := containerObj.(dt.ContainerJSON)
			pod := v1.Pod{}

			// PodMeta
			containerName := strings.TrimLeft(container.Name, "/")

			pod.Name = containerName
			pod.Namespace = "container_namespace"

			labels := container.Config.Labels
			for key, val := range labels {
				pod.Labels[key] = val
			}

			ownerRef := []metaV1.OwnerReference {
				{
					APIVersion: "apps/v1",
					Kind: "ReplicaSet",
					Name: pod.Name,
				},
			}

			pod.OwnerReferences = ownerRef

			// PodSpec

			containerImage := container.Config.Image + kl.GetSHA256ofImage(container.Image)

			var containerCmd []string
			if len(container.Config.Entrypoint) > 0 {
				containerCmd = container.Config.Entrypoint
			} else {
				containerCmd = container.Config.Cmd
			}

			var ports []v1.ContainerPort
			for n, port := range container.NetworkSettings.Ports {
				if len(port) > 0 {
					portInfo := strings.Split(string(n), "/")
					if len(portInfo) != 2 {
						continue
					}
					var k8sPort v1.ContainerPort
					for _, p := range port {
						intHPort, _ := strconv.ParseInt(p.HostPort, 10, 32)
						intCPort, _ := strconv.ParseInt(portInfo[0], 10, 32)
						k8sPort = v1.ContainerPort {
							Name: fmt.Sprintf("%s:%d", p.HostIP, portInfo[0]),
							HostPort: int32(intHPort),
							ContainerPort: int32(intCPort),
							Protocol: v1.Protocol(strings.ToUpper(portInfo[1])),
							HostIP: p.HostIP,
						}
					}

					ports = append(ports, k8sPort)
				}
			}

			var envVar []v1.EnvVar
			for _, vars := range container.Config.Env {
				//env := strings.Split(vars, "=")
				envKey, envVal, ok := strings.Cut(vars, "=")
				if !ok {
					continue
				}
				k8sEnv := v1.EnvVar {
					Name: envKey,
					Value: envVal,
				}
				envVar = append(envVar, k8sEnv)
			}

			volumes := make([]v1.Volume, len(container.Mounts))
			volumeMounts := make([]v1.VolumeMount, len(container.Mounts))

			for _, mnt := range container.Mounts {
				mntName := mnt.Source
				if mnt.Name != "" {
					mntName = mnt.Name
				}

				volSrc := v1.HostPathVolumeSource {
					Path: mnt.Source,
				}
				vol := v1.Volume {
					//Name: fmt.Sprintf("%s-bind-%d", pod.Name, i),
					Name: mntName,
					VolumeSource: v1.VolumeSource {
						HostPath: &volSrc,
					},
				}

				volMnt := v1.VolumeMount {
					Name: mnt.Name,
					ReadOnly: mnt.RW,
					MountPath: mnt.Destination,
				}

				volumes = append(volumes, vol)
				volumeMounts = append(volumeMounts, volMnt)
			}

			pod.Spec.Volumes = volumes

			addCaps := make([]v1.Capability, len(container.HostConfig.CapAdd))
			capSysAdmin := true
			for _, caps := range container.HostConfig.CapAdd {
				addCaps = append(addCaps, v1.Capability(caps))
				if caps == "CAP_SYS_ADMIN" {
					capSysAdmin = true
				}
			}

			dropCaps := make([]v1.Capability, len(container.HostConfig.CapAdd))
			for _, caps := range container.HostConfig.CapDrop {
				dropCaps = append(dropCaps, v1.Capability(caps))
			}

			securityContext := v1.SecurityContext {
				Capabilities: &v1.Capabilities{
					Add: addCaps,
					Drop: dropCaps,
				},
				Privileged: &container.HostConfig.Privileged,
				ReadOnlyRootFilesystem: &container.HostConfig.ReadonlyRootfs,
				// SELinuxOptions
				// RunAsGroup
				// ProcMount
			}

			if len(container.Config.User) > 0 {
				userInt, _ := strconv.ParseInt(container.Config.User, 10, 64)
				*securityContext.RunAsUser = userInt

				if userInt == 0 {
					*securityContext.RunAsNonRoot = false
				}
			}

			if *securityContext.Privileged || capSysAdmin {
				*securityContext.AllowPrivilegeEscalation = false
			}

			secOpts, err := dt.DecodeSecurityOptions(container.HostConfig.SecurityOpt)
			if err != nil {
				kg.Warnf("Failed to get security options for container ID %s. %s", container.ID, err)
			}
			seccompProf := v1.SeccompProfile{
				Type: v1.SeccompProfileTypeRuntimeDefault,
			}
			for _, options := range secOpts {
				for _, opt := range options.Options {
					if options.Name == "seccomp" && opt.Value == "unconfined" {
						seccompProf.Type = v1.SeccompProfileTypeUnconfined
					} else if options.Name == "seccomp" {
						// seccomp tag exists and contains a value
						seccompProf.Type = v1.SeccompProfileTypeLocalhost
					}
				}
			}

			if container.AppArmorProfile != "" && container.AppArmorProfile != "docker-default" {
				pod.Annotations[fmt.Sprintf("container.apparmor.security.beta.kubernetes.io/%s", containerName)] = container.AppArmorProfile
			}

			pod.Annotations["kubearmor-policy"] = "enabled"
			pod.Annotations["kubearmor-visibility"] = "process,file,network,capabilities"

			k8sContainer := v1.Container {
				Name: containerName,
				Image: containerImage,
				Command: containerCmd,
				Args: container.Args,
				WorkingDir: container.Config.WorkingDir,
				Ports: ports,
				Env: envVar,
				VolumeMounts: volumeMounts,
				ImagePullPolicy: v1.PullIfNotPresent,
				SecurityContext: &securityContext,
				Stdin: container.Config.OpenStdin,
				StdinOnce: container.Config.StdinOnce,
				TTY: container.Config.Tty,
			}

			if container.Node != nil {
			}

			// node name
			// to be added by aggregator
			// pod.Spec.NodeName = container.

			pod.Spec.HostNetwork = container.HostConfig.NetworkMode.IsHost()
			pod.Spec.HostPID = container.HostConfig.PidMode.IsHost()
			pod.Spec.HostIPC = container.HostConfig.IpcMode.IsHost()

			// pod sec context
			pod.Spec.Hostname = container.Config.Hostname

			supGroups := make([]int64, len(container.HostConfig.GroupAdd))
			for _, grp := range container.HostConfig.GroupAdd {
				grpInt, _ := strconv.ParseInt(grp, 10, 64)
				supGroups = append(supGroups, grpInt)
			}

			podSecContext := v1.PodSecurityContext {
				SupplementalGroups: supGroups,
			}

			pod.Spec.Subdomain = container.Config.Domainname
			pod.Spec.OS = &v1.PodOS{
				Name: v1.OSName(container.Platform),
			}
			*pod.Spec.HostUsers = container.HostConfig.Usernsaode.IsHost()

		}

		// pod state
	}

	return []byte(""), nil
}
*/
