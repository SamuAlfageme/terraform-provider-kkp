package provider

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

func TestAccProject(t *testing.T) {
	s := newMock(t)
	cfg := providerConfig(s, kkpmock.AdminToken, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(st *terraform.State) error {
			for _, r := range st.RootModule().Resources {
				for _, req := range s.Requests() {
					if req == "DELETE /api/v1/projects/"+r.Primary.ID {
						return nil
					}
				}
				return fmt.Errorf("project %s was not deleted", r.Primary.ID)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg + `
resource "kkp_project" "p" {
  name   = "platform"
  labels = { team = "infra", env = "prod" }
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_project.p", "name", "platform"),
					resource.TestCheckResourceAttr("kkp_project.p", "status", "Active"),
					resource.TestCheckResourceAttr("kkp_project.p", "labels.team", "infra"),
					resource.TestCheckResourceAttrSet("kkp_project.p", "id"),
				),
			},
			{
				Config: cfg + `
resource "kkp_project" "p" {
  name   = "platform-team"
  labels = { team = "infra" }
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_project.p", "name", "platform-team"),
					resource.TestCheckResourceAttr("kkp_project.p", "labels.%", "1"),
				),
			},
			{
				ResourceName:            "kkp_project.p",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
		},
	})
}

func TestAccProjectServiceAccountCannotCreate(t *testing.T) {
	s := newMock(t)
	token := s.ServiceAccountToken(s.AddProject("existing"))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      providerConfig(s, token, "") + `resource "kkp_project" "p" { name = "nope" }`,
			ExpectError: regexp.MustCompile(`(?s)not allowed to\s+create a project.*cannot create or delete\s+projects`),
		}},
	})
}

func TestAccSSHKey(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	token := s.ServiceAccountToken(project)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// No project_id anywhere: it comes from the service account token.
				Config: providerConfig(s, token, "") + fmt.Sprintf(`
resource "kkp_ssh_key" "k" {
  name       = "laptop"
  public_key = "${trimspace(%q)}\n"
}`, testPublicKey(1)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_ssh_key.k", "project_id", project),
					resource.TestMatchResourceAttr("kkp_ssh_key.k", "fingerprint", regexp.MustCompile(`^([0-9a-f]{2}:){15}[0-9a-f]{2}$`)),
				),
			},
			{
				ResourceName:      "kkp_ssh_key.k",
				ImportState:       true,
				ImportStateIdFunc: importID("kkp_ssh_key.k", "project_id", "id"),
				ImportStateVerify: true,
				// KKP trims the key; the configured value keeps its trailing newline.
				ImportStateVerifyIgnore: []string{"public_key"},
			},
			{
				Config: providerConfig(s, token, "") + `
resource "kkp_ssh_key" "bad" {
  name       = "bad"
  public_key = "not a key"
}`,
				ExpectError: regexp.MustCompile(`ssh key is\s+invalid`),
			},
		},
	})
}

func TestAccServiceAccountAndToken(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, kkpmock.AdminToken, project)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg + `
resource "kkp_service_account" "ci" {
  name  = "ci"
  group = "editors"
}
resource "kkp_service_account_token" "ci" {
  service_account_id = kkp_service_account.ci.id
  name               = "github-actions"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_service_account.ci", "group", "editors"),
					resource.TestMatchResourceAttr("kkp_service_account_token.ci", "token", regexp.MustCompile(`^[\w-]+\.[\w-]+\.[\w-]+$`)),
					resource.TestCheckResourceAttrSet("kkp_service_account_token.ci", "expiry"),
				),
			},
			{
				Config: cfg + `
resource "kkp_service_account" "ci" {
  name  = "ci-readonly"
  group = "viewers"
}
resource "kkp_service_account_token" "ci" {
  service_account_id = kkp_service_account.ci.id
  name               = "github-actions-2"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_service_account.ci", "group", "viewers"),
					resource.TestCheckResourceAttr("kkp_service_account_token.ci", "name", "github-actions-2"),
				),
			},
			{
				ResourceName:      "kkp_service_account.ci",
				ImportState:       true,
				ImportStateIdFunc: importID("kkp_service_account.ci", "project_id", "id"),
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccProjectMember(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, kkpmock.AdminToken, project)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg + `resource "kkp_project_member" "m" {
  email = "jane@example.com"
  group = "viewers"
}`,
				Check: resource.TestCheckResourceAttr("kkp_project_member.m", "group", "viewers"),
			},
			{
				Config: cfg + `resource "kkp_project_member" "m" {
  email = "jane@example.com"
  group = "editors"
}`,
				Check: resource.TestCheckResourceAttr("kkp_project_member.m", "group", "editors"),
			},
			{
				ResourceName:      "kkp_project_member.m",
				ImportState:       true,
				ImportStateIdFunc: importID("kkp_project_member.m", "project_id", "id"),
				ImportStateVerify: true,
			},
		},
	})
}

// testPublicKey returns a well-formed ed25519 authorized_keys line, different for each seed.
func testPublicKey(seed byte) string {
	blob := []byte{0, 0, 0, 11}
	blob = append(blob, "ssh-ed25519"...)
	blob = append(blob, 0, 0, 0, 32)
	for i := 0; i < 32; i++ {
		blob = append(blob, seed+byte(i))
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " test@example.com"
}

// importID joins attributes of a resource in state with "/".
func importID(name string, attrs ...string) resource.ImportStateIdFunc {
	return func(st *terraform.State) (string, error) {
		r, ok := st.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("%s not in state", name)
		}
		id := ""
		for i, a := range attrs {
			if i > 0 {
				id += "/"
			}
			id += r.Primary.Attributes[a]
		}
		return id, nil
	}
}
