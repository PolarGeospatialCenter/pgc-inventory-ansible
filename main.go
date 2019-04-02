package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"

	"github.com/PolarGeospatialCenter/inventory-client/pkg/api/client"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/spf13/viper"
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

func main() {

	cfg := viper.New()
	cfg.AddConfigPath(".")
	cfg.SetConfigName("pgc-inventory")
	cfg.SetDefault("aws.region", "us-east-2")
	cfg.SetDefault("aws.profile", "default")
	cfg.ReadInConfig()

	awsConfig := &aws.Config{}
	awsConfig.WithRegion(cfg.GetString("aws.region"))
	awsConfig.WithCredentials(credentials.NewSharedCredentials("", cfg.GetString("aws.profile")))

	baseUrlString := cfg.GetString("baseurl")
	baseUrl, err := url.Parse(baseUrlString)
	if err != nil {
		log.Fatalf("unable to parse api base url '%s': %v", baseUrlString, err)
	}

	nodes, err := client.NewInventoryApi(baseUrl, awsConfig).NodeConfig().GetAll()
	if err != nil {
		log.Fatalf("Unable to read nodes: %v", err)
	}

	groups := make(AnsibleGroupList)
	hostVars := make(map[string]map[string]interface{})

	for _, node := range nodes {
		var domain string
		if provNet, ok := node.Networks["provisioning"]; ok {
			domain = provNet.Network.Domain
		}
		if cpNetworkName, ok := node.Environment.Metadata["kubernetes_control_plane_network"].(string); ok {
			if cpNetwork, ok := node.Networks[cpNetworkName]; ok {
				domain = cpNetwork.Network.Domain
			}
		}

		fqdn := fmt.Sprintf("%s.%s", node.Hostname, domain)
		group := groups.Get(node.System.ID())
		group.AddHost(fqdn)
		roleGroup := fmt.Sprintf("%s-%s", node.System.ID(), node.Role)
		group = groups.Get(roleGroup)
		groups[roleGroup].AddHost(fqdn)
		hostVars[fqdn] = make(map[string]interface{})
		hostVars[fqdn]["ansible_python_interpreter"] = "/opt/ansible/bin/python"
		hostVars[fqdn]["tags"] = node.Tags
		hostVars[fqdn]["inventory_id"] = node.ID()
		hostVars[fqdn]["rack"] = node.Location.Rack
		hostVars[fqdn]["role"] = node.Role
		hostVars[fqdn]["last_update"] = node.LastUpdated
		hostVars[fqdn]["nodeconfig"] = node
		if cpNetworkName, ok := node.Environment.Metadata["kubernetes_control_plane_network"].(string); ok {
			if cpNetwork, ok := node.Networks[cpNetworkName]; ok {
				hostVars[fqdn]["kube_control_plane_domain"] = cpNetwork.Network.Domain
				hostVars[fqdn]["kube_control_plane_ips"] = make([]string, 0, len(cpNetwork.Config.IP))
				for _, ipString := range cpNetwork.Config.IP {
					ip, _, err := net.ParseCIDR(ipString)
					if err == nil {
						hostVars[fqdn]["kube_control_plane_ips"] = append(hostVars[fqdn]["kube_control_plane_ips"].([]string), ip.String())
					}
				}
			}
		}
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
