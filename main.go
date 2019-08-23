package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"

	"github.com/PolarGeospatialCenter/inventory-client/pkg/api/client"
	"github.com/PolarGeospatialCenter/inventory/pkg/inventory/types"
)

type AnsibleGroup struct {
	Hosts []string               `json:"hosts"`
	Vars  map[string]interface{} `json:"vars"`
}

type AnsibleGroupList map[string]*AnsibleGroup

func (l *AnsibleGroupList) Get(groupname string) *AnsibleGroup {
	group, ok := (*l)[groupname]
	if !ok {
		group = &AnsibleGroup{Hosts: []string{}, Vars: make(map[string]interface{})}
		(*l)[groupname] = group
	}
	return group
}

func (g *AnsibleGroup) AddHost(hostname string) {
	g.Hosts = append(g.Hosts, hostname)
}

type AnsibleHost types.InventoryNode

func (h AnsibleHost) AnsibleHostAlias() (string, error) {
	return h.Hostname, nil
}

// AnsibleConnectionData returns ansible_fqdn, ansible_host and ansible_port host vars
func (h AnsibleHost) AnsibleConnectionData() (string, string, string, error) {
	var domain string
	var ipAddr net.IP

	for _, network := range h.Networks {
		conf := network.Config
		for i, gw := range conf.Gateway {
			if gwIP := net.ParseIP(gw); gwIP != nil {
				ip, _, err := net.ParseCIDR(conf.IP[i])
				if err == nil {
					ipAddr = ip
				}
				domain = network.Network.Domain
			}
		}
	}

	var fqdn, host, port string
	if domain != "" {
		fqdn = fmt.Sprintf("%s.%s", h.Hostname, domain)
	}

	switch {
	case fqdn != "":
		host = fqdn
	case ipAddr != nil:
		host = ipAddr.String()
	default:
		host = h.Hostname
	}

	port = "22"

	return fqdn, host, port, nil
}

func (h AnsibleHost) AnsibleGroups() ([]string, error) {
	groups := make([]string, 0)
	groups = append(groups, h.System.ID())
	groups = append(groups, fmt.Sprintf("%s-%s", h.System.ID(), h.Role))
	return groups, nil
}

func (h AnsibleHost) AnsibleHostVars() (map[string]interface{}, error) {
	hostVars := make(map[string]interface{})
	hostVars["tags"] = h.Tags
	hostVars["inventory_id"] = h.InventoryID
	hostVars["rack"] = h.Location.Rack
	hostVars["role"] = h.Role
	hostVars["last_update"] = h.LastUpdated
	hostVars["nodeconfig"] = h
	fqdn, host, port, err := h.AnsibleConnectionData()
	if err != nil {
		return nil, err
	}
	hostVars["ansible_fqdn"] = fqdn
	hostVars["ansible_host"] = host
	hostVars["ansible_port"] = port
	if cpNetworkName, ok := h.Environment.Metadata["kubernetes_control_plane_network"].(string); ok {
		if cpNetwork, ok := h.Networks[cpNetworkName]; ok {
			hostVars["kube_control_plane_domain"] = cpNetwork.Network.Domain
			hostVars["kube_control_plane_ips"] = make([]string, 0, len(cpNetwork.Config.IP))
			for _, ipString := range cpNetwork.Config.IP {
				ip, _, err := net.ParseCIDR(ipString)
				if err == nil {
					hostVars["kube_control_plane_ips"] = append(hostVars["kube_control_plane_ips"].([]string), ip.String())
				}
			}
		}
	}
	return hostVars, nil
}

func main() {
	apiClient, _ := client.NewInventoryApiDefaultConfig("default")
	nodes, err := apiClient.NodeConfig().GetAll()
	if err != nil {
		log.Fatalf("Unable to read nodes: %v", err)
	}

	groups := make(AnsibleGroupList)
	hostVars := make(map[string]map[string]interface{})

	for _, node := range nodes {
		ansibleNode := AnsibleHost(*node)
		alias, err := ansibleNode.AnsibleHostAlias()
		if err != nil {
			// Unable to get fqdn, how to fallback?
			log.Printf("Unable to get FQDN for node %s: %v", node.ID(), err)
			continue
		}

		hostGroups, err := ansibleNode.AnsibleGroups()
		if err != nil {
			log.Printf("Unable to get groups for node %s: %v", node.ID(), err)
		}
		for _, groupName := range hostGroups {
			group := groups.Get(groupName)
			group.AddHost(alias)
		}

		vars, err := ansibleNode.AnsibleHostVars()
		if err != nil {
			log.Printf("Unable to get vars for host %s: %v", node.ID(), err)
		}
		hostVars[alias] = vars
	}

	result := make(map[string]interface{})
	for gName, group := range groups {
		result[gName] = group
	}
	result["_meta"] = make(map[string]interface{})
	result["_meta"].(map[string]interface{})["hostvars"] = hostVars

	txt, err := json.Marshal(result)
	if err != nil {
		log.Fatalf("Unable to marshal group data: %v", err)
	}
	fmt.Printf(string(txt))
}
