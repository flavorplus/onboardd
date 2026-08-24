package config

import (
	"strings"
	"testing"
)

func TestRenderTemplatesUsesOnlyDocumentedValues(t *testing.T) {
	configured := Defaults()
	configured.Product.Name = "InkyPi"
	configured.Product.DeviceName = "Kitchen"
	configured.Branding.Text.Title = "Set up {{ .DeviceName }}"
	configured.Branding.Text.Subtitle = "Open {{.Hostname}} when setup finishes."
	configured.Network.Provisioning.SSID = "{{ .ProductName }}-Setup-{{ .DeviceID }}"
	configured.Network.Standalone.SSID = "{{.DeviceName}}-{{.DeviceID}}"
	configured.Handoff.ApplicationLabel = "Open {{ .ProductName }}"
	configured.Handoff.ApplicationURL = "http://{{ .Hostname }}.local/"
	configured.Handoff.HealthCheckURL = "http://127.0.0.1/{{ .DeviceID }}/health"

	rendered, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "inkypi"})
	if err != nil {
		t.Fatalf("RenderTemplates() error = %v", err)
	}
	checks := map[string]string{
		"title":             rendered.Branding.Text.Title,
		"subtitle":          rendered.Branding.Text.Subtitle,
		"provisioning SSID": rendered.Network.Provisioning.SSID,
		"standalone SSID":   rendered.Network.Standalone.SSID,
		"application label": rendered.Handoff.ApplicationLabel,
		"application URL":   rendered.Handoff.ApplicationURL,
		"health URL":        rendered.Handoff.HealthCheckURL,
	}
	want := map[string]string{
		"title":             "Set up Kitchen",
		"subtitle":          "Open inkypi when setup finishes.",
		"provisioning SSID": "InkyPi-Setup-AB12CD34",
		"standalone SSID":   "Kitchen-AB12CD34",
		"application label": "Open InkyPi",
		"application URL":   "http://inkypi.local/",
		"health URL":        "http://127.0.0.1/AB12CD34/health",
	}
	for name, value := range checks {
		if value != want[name] {
			t.Errorf("%s = %q, want %q", name, value, want[name])
		}
	}
}

func TestRenderTemplatesRejectsUnknownField(t *testing.T) {
	configured := Defaults()
	configured.Branding.Text.Title = "{{ .Environment }}"
	_, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "display"})
	if err == nil || !strings.Contains(err.Error(), `unknown template field "Environment"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderTemplatesRejectsFunctionsAndPipelines(t *testing.T) {
	for _, expression := range []string{
		`{{ printf "%s" .DeviceID }}`,
		`{{ .DeviceID | printf "%s" }}`,
		`{{ if .DeviceID }}yes{{ end }}`,
	} {
		configured := Defaults()
		configured.Branding.Text.Title = expression
		_, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "display"})
		if err == nil || !strings.Contains(err.Error(), "template") {
			t.Errorf("expression %q error = %v", expression, err)
		}
	}
}

func TestRenderTemplatesRejectsSSIDOver32Bytes(t *testing.T) {
	configured := Defaults()
	configured.Product.Name = strings.Repeat("x", 24)
	_, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "display"})
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderTemplatesRejectsControlCharacterInSSID(t *testing.T) {
	configured := Defaults()
	configured.Network.Provisioning.SSID = "Setup\nNetwork"
	_, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "display"})
	if err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderTemplatesDoesNotRenderDisabledStandaloneSSID(t *testing.T) {
	configured := Defaults()
	configured.Network.StandaloneEnabled = false
	configured.Network.Standalone.SSID = "{{ .Unknown }}"
	if _, err := RenderTemplates(configured, Identity{DeviceID: "AB12CD34", Hostname: "display"}); err != nil {
		t.Fatalf("RenderTemplates() error = %v", err)
	}
}
