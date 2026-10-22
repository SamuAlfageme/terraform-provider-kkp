package kkpmock

import "strings"

// mockCloud is what the fake knows about one cloud provider: how KKP validates credentials, which
// resources its cloud controller fills in, and which machine fields machine-controller requires.
type mockCloud struct {
	// Credentials holds the accepted credential sets; any complete one is valid.
	Credentials [][]string
	// Secret lists every credential field; KKP moves them into a secret and drops them from the spec.
	Secret []string
	// Reconciled are the cloud resources KKP creates when unset, by field.
	Reconciled func(clusterID string, dc map[string]any) map[string]any
	// NodeRequired are the machine fields machine-controller refuses to do without.
	NodeRequired []string
	// NodeDefaults fills in machine fields KKP defaults.
	NodeDefaults func(dc, clusterCloud map[string]any, osName string) map[string]any
}

var clouds = map[string]mockCloud{
	"openstack": {
		Credentials: [][]string{
			{"username", "password", "domain", "project"},
			{"username", "password", "domain", "projectID"},
			{"applicationCredentialID", "applicationCredentialSecret"},
		},
		Secret: []string{"username", "password", "domain", "project", "projectID", "applicationCredentialID", "applicationCredentialSecret", "token"},
		Reconciled: func(id string, _ map[string]any) map[string]any {
			return map[string]any{"network": "kubernetes-" + id, "subnetID": "subnet-" + id, "routerID": "router-" + id,
				"securityGroups": "kubernetes-" + id, "floatingIPPool": "public"}
		},
		NodeRequired: []string{"flavor", "image"},
	},
	"aws": {
		Credentials: [][]string{{"accessKeyID", "secretAccessKey"}},
		Secret:      []string{"accessKeyID", "secretAccessKey", "assumeRoleARN", "assumeRoleExternalID"},
		Reconciled: func(id string, _ map[string]any) map[string]any {
			return map[string]any{"vpcID": "vpc-0a1b2c3d", "securityGroupID": "sg-" + id, "routeTableID": "rtb-0a1b2c3d",
				"instanceProfileName": "kubernetes-" + id, "roleARN": "arn:aws:iam::123456789012:role/kubernetes-" + id}
		},
		NodeRequired: []string{"instanceType", "diskSize", "volumeType"},
		NodeDefaults: func(dc, _ map[string]any, osName string) map[string]any {
			region := str(dc, "spec", "aws", "region")
			return map[string]any{"ami": str(dc, "spec", "aws", "images", osName), "availabilityZone": region + "a", "subnetID": "subnet-0a1b2c3d"}
		},
	},
	"azure": {
		Credentials: [][]string{{"tenantID", "subscriptionID", "clientID", "clientSecret"}},
		Secret:      []string{"tenantID", "subscriptionID", "clientID", "clientSecret"},
		Reconciled: func(id string, _ map[string]any) map[string]any {
			rg := "cluster-" + id
			return map[string]any{"resourceGroup": rg, "vnetResourceGroup": rg, "vnet": rg, "subnet": rg, "routeTable": rg,
				"securityGroup": rg, "availabilitySet": rg, "loadBalancerSKU": "standard"}
		},
		NodeRequired: []string{"size"},
		NodeDefaults: func(dc, _ map[string]any, osName string) map[string]any {
			return map[string]any{"imageID": str(dc, "spec", "azure", "images", osName)}
		},
	},
	"gcp": {
		Credentials: [][]string{{"serviceAccount"}},
		Secret:      []string{"serviceAccount"},
		Reconciled: func(string, map[string]any) map[string]any {
			return map[string]any{"network": "global/networks/default", "subnetwork": ""}
		},
		NodeRequired: []string{"machineType", "diskSize", "diskType"},
		NodeDefaults: func(dc, _ map[string]any, _ string) map[string]any {
			return map[string]any{"zone": str(dc, "spec", "gcp", "region") + "-a"}
		},
	},
	"hetzner": {
		Credentials: [][]string{{"token"}},
		Secret:      []string{"token"},
		Reconciled: func(_ string, dc map[string]any) map[string]any {
			return map[string]any{"network": str(dc, "spec", "hetzner", "network")}
		},
		NodeRequired: []string{"type"},
		NodeDefaults: func(_, cloud map[string]any, _ string) map[string]any {
			return map[string]any{"network": str(cloud, "network")}
		},
	},
	"digitalocean": {
		Credentials:  [][]string{{"token"}},
		Secret:       []string{"token"},
		NodeRequired: []string{"size"},
	},
}

func (m mockCloud) validCredentials(spec map[string]any) bool {
	for _, set := range m.Credentials {
		ok := true
		for _, k := range set {
			v, _ := spec[k].(string)
			ok = ok && v != ""
		}
		if ok {
			return true
		}
	}
	return false
}

func (m mockCloud) credentialHelp() string {
	var alts []string
	for _, set := range m.Credentials {
		alts = append(alts, strings.Join(set, ", "))
	}
	return strings.Join(alts, "; or ")
}
