package types

type ContainerDetails struct {
	ContainerName string `json:"container_name,omitempty"`
	NameOfService string `json:"name_of_service,omitempty"`
	Image         string `json:"image,omitempty"`
	Status        string `json:"status,omitempty"`
	ProtocolPort  string `json:"protocol_port,omitempty"`
	ContainerId   string `json:"containerId,omitempty"`
}

type PodDetails struct {
	ClusterId       int64               `json:"cluster_id,omitempty"`
	NewPodName      string              `json:"new_pod_name,omitempty"`
	OldPodName      string              `json:"old_pod_name,omitempty"`
	Namespace       string              `json:"namespace,omitempty"`
	NodeName        string              `json:"node_name,omitempty"`
	LastUpdatedTime string              `json:"last_updated_time,omitempty"`
	Container       []*ContainerDetails `json:"container,omitempty"`
	Labels          []*Labels           `json:"labels,omitempty"`
	Operation       string              `json:"operation,omitempty"`
	PodIp           string              `json:"pod_ip,omitempty"`
	WorkspaceId     int64               `json:"workspace_id,omitempty"`
	WorkloadName    string              `json:"workloadName,omitempty"`
	WorkloadType    string              `json:"workloadType,omitempty"`
}

type NodeDetails struct {
	ClusterId       int64     `json:"cluster_id,omitempty"`
	NewNodeName     string    `json:"new_node_name,omitempty"`
	OldNodeName     string    `json:"old_node_name,omitempty"`
	LastUpdatedTime string    `json:"last_updated_time,omitempty"`
	Labels          []*Labels `json:"labels,omitempty"`
	Operation       string    `json:"operation,omitempty"`
	WorkspaceId     int64     `json:"workspace_id,omitempty"`
}

type NamespaceDetails struct {
	ClusterId               int64     `json:"cluster_id,omitempty"`
	NewNamespaceName        string    `json:"new_namespace_name,omitempty"`
	OldNamespaceName        string    `json:"old_namespace_name,omitempty"`
	LastUpdatedTime         string    `json:"last_updated_time,omitempty"`
	Operation               string    `json:"operation,omitempty"`
	Labels                  []*Labels `json:"labels,omitempty"`
	WorkspaceId             int64     `json:"workspace_id,omitempty"`
	KubearmorFilePosture    string    `json:"kubearmor_file_posture,omitempty"`
	KubearmorNetworkPosture string    `json:"kubearmor_network_posture,omitempty"`
}

type Labels struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}
