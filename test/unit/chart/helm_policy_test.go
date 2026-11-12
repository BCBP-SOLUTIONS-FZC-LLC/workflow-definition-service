package chart_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	chartDir                    = "../../../deploy/helm"
	routerFile                  = "../../../internal/adapter/inbound/http/router.go"
	authorizationPolicyTemplate = "templates/authorizationpolicy.yaml"
	networkPolicyTemplate       = "templates/networkpolicy.yaml"
	internalPrefix              = "/api/v1/internal"
	releaseNamespace            = "workflow-app"
)

// prodSettings are what a production render needs: the chart refuses to render without them.
var prodSettings = []string{
	"--set", "existingSecret=workflow-definition-service-secrets",
	"--set-string", "env.ORG_MEMBERSHIP_BASE_URL=http://iam-org-membership.iam.svc.cluster.local:8080",
	"--set-string", "env.CATALOG_ADMIN_BASE_URL=http://iam-catalog-admin.iam.svc.cluster.local:8081",
	"--set-string", "env.OPENBAO_ADDR=http://openbao.platform.svc:8200",
	"--set-string", "env.VALKEY_ADDR=valkey.platform.svc:6379",
}

type chartObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Ports []struct {
			Name        string `yaml:"name"`
			Port        int    `yaml:"port"`
			AppProtocol string `yaml:"appProtocol"`
		} `yaml:"ports"`
		Template struct {
			Spec struct {
				ServiceAccountName string `yaml:"serviceAccountName"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type policyRule struct {
	From []struct {
		Source struct {
			Principals []string `yaml:"principals"`
		} `yaml:"source"`
	} `yaml:"from"`
	To []struct {
		Operation struct {
			Paths    []string `yaml:"paths"`
			NotPaths []string `yaml:"notPaths"`
		} `yaml:"operation"`
	} `yaml:"to"`
}

type authorizationPolicy struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"selector"`
		Action string       `yaml:"action"`
		Rules  []policyRule `yaml:"rules"`
	} `yaml:"spec"`
}

type labelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

type peer struct {
	PodSelector       *labelSelector `yaml:"podSelector"`
	NamespaceSelector *labelSelector `yaml:"namespaceSelector"`
}

type netRule struct {
	From  []peer `yaml:"from"`
	To    []peer `yaml:"to"`
	Ports []struct {
		Port int `yaml:"port"`
	} `yaml:"ports"`
}

type networkPolicy struct {
	Spec struct {
		Ingress []netRule `yaml:"ingress"`
		Egress  []netRule `yaml:"egress"`
	} `yaml:"spec"`
}

// helmRender runs `helm template` for a release with the production settings and returns the output.
func helmRender(t *testing.T, release string, extra ...string) (string, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	args := append([]string{"template", release, chartDir, "--namespace", releaseNamespace}, prodSettings...)
	out, err := exec.Command(helm, append(args, extra...)...).CombinedOutput()
	return string(out), err
}

// decodeAll decodes every document of a render.
func decodeAll[T any](t *testing.T, out string) []T {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(out))
	var docs []T
	for {
		var doc T
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}
		require.NoError(t, err, out)
		docs = append(docs, doc)
	}
}

func renderPolicy(t *testing.T, release string, extra ...string) authorizationPolicy {
	t.Helper()
	out, err := helmRender(t, release, append([]string{"-s", authorizationPolicyTemplate}, extra...)...)
	require.NoError(t, err, out)
	docs := decodeAll[authorizationPolicy](t, out)
	require.Len(t, docs, 1)
	return docs[0]
}

func renderNetworkPolicy(t *testing.T, release string, extra ...string) networkPolicy {
	t.Helper()
	out, err := helmRender(t, release, append([]string{"-s", networkPolicyTemplate}, extra...)...)
	require.NoError(t, err, out)
	docs := decodeAll[networkPolicy](t, out)
	require.Len(t, docs, 1)
	return docs[0]
}

func writeValues(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func principal(namespace, serviceAccount string) string {
	return "cluster.local/ns/" + namespace + "/sa/" + serviceAccount
}

// wantRule is one ALLOW rule on an internal route group: its callers and its paths together.
type wantRule struct {
	name       string
	principals []string
	paths      []string
}

func defaultInternalRules() []wantRule {
	return []wantRule{
		{"events", []string{principal("workflow-app", "workflow-events-consumer")}, []string{"/api/v1/internal/events"}},
		{"compiled-plans", []string{
			principal("workflow-app", "workflow-execution-service-api"),
			principal("workflow-app", "workflow-execution-service-worker"),
		}, []string{"/api/v1/internal/workflow-versions/*"}},
		{"connector-alias-read", []string{principal("workflow-app", "workflow-execution-service-connector-worker")}, []string{"/api/v1/internal/connector-aliases"}},
		{"connector-alias-write", []string{principal("tender-app", "tender-service")}, []string{"/api/v1/internal/connector-aliases/*"}},
	}
}

// policyProblems compares a decoded policy with the rules it must have: rule 1 admits what is
// outside the internal routes with no source, and every other rule carries one group's principals
// and paths together.
func policyProblems(rules []policyRule, want []wantRule) []string {
	if len(rules) != len(want)+1 {
		return []string{"rule count differs from the expected groups plus the admit-everything-else rule"}
	}
	var problems []string
	first := rules[0]
	if len(first.From) != 0 || len(first.To) != 1 ||
		!equalStrings(first.To[0].Operation.NotPaths, []string{"/api/v1/internal/*"}) || len(first.To[0].Operation.Paths) != 0 {
		problems = append(problems, "rule 1 must be only to.operation.notPaths [/api/v1/internal/*]")
	}
	for i, w := range want {
		r := rules[i+1]
		if len(r.From) != 1 || len(r.To) != 1 {
			problems = append(problems, w.name+": an internal rule must hold one from and one to")
			continue
		}
		if !equalStrings(r.From[0].Source.Principals, w.principals) {
			problems = append(problems, w.name+": principals differ")
		}
		if !equalStrings(r.To[0].Operation.Paths, w.paths) || len(r.To[0].Operation.NotPaths) != 0 {
			problems = append(problems, w.name+": paths differ")
		}
	}
	return problems
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHelmAuthorizationPolicy_RendersExactlyTheExpectedRules(t *testing.T) {
	p := renderPolicy(t, "wf")

	assert.Equal(t, "security.istio.io/v1", p.APIVersion)
	assert.Equal(t, "AuthorizationPolicy", p.Kind)
	assert.Equal(t, "ALLOW", p.Spec.Action)
	assert.Equal(t, "workflow-definition-service", p.Metadata.Name)
	assert.Empty(t, policyProblems(p.Spec.Rules, defaultInternalRules()))
}

func TestHelmAuthorizationPolicy_RuleOneAdmitsOnlyTheNonInternalPaths(t *testing.T) {
	p := renderPolicy(t, "wf")
	require.NotEmpty(t, p.Spec.Rules)

	assert.Empty(t, p.Spec.Rules[0].From, "rule 1 names no source: the gateway, probes and the scrape must stay admitted")
	require.Len(t, p.Spec.Rules[0].To, 1)
	assert.Equal(t, []string{"/api/v1/internal/*"}, p.Spec.Rules[0].To[0].Operation.NotPaths)
	assert.Empty(t, p.Spec.Rules[0].To[0].Operation.Paths)
}

func TestHelmAuthorizationPolicy_SelectsTheAPIPodsOfTheRelease(t *testing.T) {
	p := renderPolicy(t, "wf")

	assert.Equal(t, map[string]string{
		"app.kubernetes.io/name":     "workflow-definition-service",
		"app.kubernetes.io/instance": "wf",
	}, p.Spec.Selector.MatchLabels)
}

func TestHelmAuthorizationPolicy_AGroupWithNoCallersRendersNoRule(t *testing.T) {
	values := writeValues(t, `authorizationPolicy:
  internalRoutes:
    - name: events
      paths: ["/api/v1/internal/events"]
      callers: []
    - name: compiled-plans
      paths: ["/api/v1/internal/workflow-versions/*"]
      callers:
        - {namespace: workflow-app, serviceAccount: workflow-execution-service-api}
`)
	p := renderPolicy(t, "wf", "-f", values)

	want := []wantRule{{"compiled-plans", []string{principal("workflow-app", "workflow-execution-service-api")}, []string{"/api/v1/internal/workflow-versions/*"}}}
	assert.Empty(t, policyProblems(p.Spec.Rules, want), "the events group has no caller, so no rule admits it")
}

// The rule check must fail on the unsafe shapes it exists to catch.
func TestHelmAuthorizationPolicy_TheRuleCheckCatchesPrincipalsAndPathsSplitAcrossRules(t *testing.T) {
	p := renderPolicy(t, "wf")
	want := defaultInternalRules()
	require.Empty(t, policyProblems(p.Spec.Rules, want))

	t.Run("principals in one rule, paths in another", func(t *testing.T) {
		original := p.Spec.Rules[1]
		principalsOnly := policyRule{From: original.From}
		pathsOnly := policyRule{To: original.To}
		mutated := append([]policyRule{p.Spec.Rules[0], principalsOnly, pathsOnly}, p.Spec.Rules[2:]...)

		assert.NotEmpty(t, policyProblems(mutated, want))
	})
	t.Run("the internal paths opened to every caller", func(t *testing.T) {
		mutated := append([]policyRule(nil), p.Spec.Rules...)
		mutated[1] = policyRule{To: mutated[1].To}

		assert.NotEmpty(t, policyProblems(mutated, want))
	})
	t.Run("a probe path added to the notPaths list", func(t *testing.T) {
		mutated := append([]policyRule(nil), p.Spec.Rules...)
		mutated[0].To = append(mutated[0].To[:0:0], mutated[0].To...)
		mutated[0].To[0].Operation.NotPaths = []string{"/api/v1/internal/*", "/healthz"}

		assert.NotEmpty(t, policyProblems(mutated, want))
	})
	t.Run("a caller listed for the wrong group", func(t *testing.T) {
		wrong := append([]wantRule(nil), want...)
		wrong[2] = wantRule{"connector-alias-read", []string{principal("tender-app", "tender-service")}, want[2].paths}

		assert.NotEmpty(t, policyProblems(p.Spec.Rules, wrong))
	})
}

func TestHelmAuthorizationPolicy_IsNotRenderedWhenDisabled(t *testing.T) {
	out, err := helmRender(t, "wf", "-s", authorizationPolicyTemplate, "--set", "authorizationPolicy.enabled=false", "--set-string", "env.APP_ENV=dev")
	require.Error(t, err)
	assert.Contains(t, out, "could not find template "+authorizationPolicyTemplate, "a disabled policy renders nothing, so helm finds no template to show")

	out, err = helmRender(t, "wf", "--set", "authorizationPolicy.enabled=false", "--set-string", "env.APP_ENV=dev")
	require.NoError(t, err, out)
	assert.NotContains(t, out, "kind: AuthorizationPolicy")
}

func TestHelmAuthorizationPolicy_TheRenderRefusesAPolicyThatIsOffInProdAndStaging(t *testing.T) {
	for _, env := range []string{"prod", "staging"} {
		out, err := helmRender(t, "wf", "--set", "authorizationPolicy.enabled=false", "--set-string", "env.APP_ENV="+env)
		require.Error(t, err, "env.APP_ENV=%s with the policy off leaves the internal routes to the NetworkPolicy alone", env)
		assert.Contains(t, out, "authorizationPolicy.allowDisabled", env)

		out, err = helmRender(t, "wf", "--set", "authorizationPolicy.enabled=false", "--set", "authorizationPolicy.allowDisabled=true", "--set-string", "env.APP_ENV="+env)
		require.NoError(t, err, out)
		assert.NotContains(t, out, "kind: AuthorizationPolicy")
	}
	for _, env := range []string{"local", "dev", "test"} {
		out, err := helmRender(t, "wf", "--set", "authorizationPolicy.enabled=false", "--set-string", "env.APP_ENV="+env)
		require.NoError(t, err, out)
	}
	out, err := helmRender(t, "wf", "--set-string", "env.APP_ENV=prod")
	require.NoError(t, err, out, "the policy on is the default and renders in prod")
}

func TestHelmNotes_StateTheMeshRequirement(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(chartDir, "templates", "NOTES.txt"))
	require.NoError(t, err)
	notes := string(body)

	assert.Contains(t, notes, "sidecar")
	assert.Contains(t, notes, "mutual TLS")
	assert.Contains(t, notes, "refused on the internal paths")
	assert.Contains(t, notes, "PeerAuthentication")
	assert.Contains(t, notes, "Platform")
}

func TestHelmNames_TheServiceAndServiceAccountDoNotDependOnTheReleaseName(t *testing.T) {
	for _, release := range []string{"wf", "definition", "a-much-longer-release-name"} {
		out, err := helmRender(t, release)
		require.NoError(t, err, out)

		var services, accounts, workloads []string
		for _, o := range decodeAll[chartObject](t, out) {
			switch o.Kind {
			case "Service":
				services = append(services, o.Metadata.Name)
			case "ServiceAccount":
				accounts = append(accounts, o.Metadata.Name)
			case "Deployment", "Job":
				workloads = append(workloads, o.Spec.Template.Spec.ServiceAccountName)
			}
		}

		assert.Equal(t, []string{"workflow-definition-service"}, services, release)
		assert.Equal(t, []string{"workflow-definition-service"}, accounts, release)
		assert.NotEmpty(t, workloads, release)
		for _, name := range workloads {
			assert.Equal(t, "workflow-definition-service", name, "%s: the pods run as the ServiceAccount the callers' policies name", release)
		}
	}
}

func TestHelmNames_TheAddressesInTheValuesAgreeWithTheChartsNames(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	require.NoError(t, err)
	var values struct {
		Service struct {
			Ports struct {
				HTTP int `yaml:"http"`
			} `yaml:"ports"`
		} `yaml:"service"`
		Env struct {
			ExecutionServiceURL string `yaml:"EXECUTION_SERVICE_URL"`
		} `yaml:"env"`
	}
	require.NoError(t, yaml.Unmarshal(body, &values))

	assert.Equal(t, 8080, values.Service.Ports.HTTP, "the event consumer and Execution address http://workflow-definition-service:8080")
	assert.Equal(t, "http://workflow-execution-service-api:8080", values.Env.ExecutionServiceURL)
}

func TestHelmServices_TheHTTPAndMetricsPortsAreNamedAsHTTP(t *testing.T) {
	out, err := helmRender(t, "wf")
	require.NoError(t, err, out)

	seen := 0
	for _, o := range decodeAll[chartObject](t, out) {
		if o.Kind != "Service" {
			continue
		}
		for _, port := range o.Spec.Ports {
			assert.Equal(t, "http", port.AppProtocol, "port %s: without it a mesh may treat the port as TCP and path rules match nothing", port.Name)
			seen++
		}
	}
	assert.Equal(t, 2, seen, "the http and metrics ports")
}

func egressToPods(np networkPolicy, name, component string) []netRule {
	var rules []netRule
	for _, r := range np.Spec.Egress {
		for _, to := range r.To {
			if to.PodSelector != nil && to.PodSelector.MatchLabels["app.kubernetes.io/name"] == name &&
				to.PodSelector.MatchLabels["app.kubernetes.io/component"] == component {
				rules = append(rules, r)
			}
		}
	}
	return rules
}

func TestHelmNetworkPolicy_EgressToTheExecutionAPIIsLimitedToItsPort(t *testing.T) {
	rules := egressToPods(renderNetworkPolicy(t, "wf"), "workflow-execution-service", "api")

	require.Len(t, rules, 1)
	require.Len(t, rules[0].Ports, 1, "no ports would admit every port of the Execution API pods")
	assert.Equal(t, 8080, rules[0].Ports[0].Port)
}

// callerPods names the pods behind each mesh caller of the internal routes: the label the NetworkPolicy
// must match and the namespace it runs in. A caller the table does not know fails the test below, so a new
// caller is added here and to networkPolicy.allowedCallers together.
var callerPods = map[string]struct{ namespace, name string }{
	"workflow-events-consumer":                    {releaseNamespace, "workflow-events-consumer"},
	"workflow-execution-service-api":              {releaseNamespace, "workflow-execution-service"},
	"workflow-execution-service-worker":           {releaseNamespace, "workflow-execution-service"},
	"workflow-execution-service-connector-worker": {releaseNamespace, "workflow-execution-service"},
	"tender-service":                              {"tender-app", "tender-service"},
}

// admitsPod says whether the rule's peers admit a pod with this name label in this namespace, on the port.
func admitsPod(rule netRule, namespace, name string, port int) bool {
	onPort := false
	for _, p := range rule.Ports {
		onPort = onPort || p.Port == port
	}
	if !onPort {
		return false
	}
	for _, from := range rule.From {
		if from.PodSelector == nil || from.PodSelector.MatchLabels["app.kubernetes.io/name"] != name {
			continue
		}
		if from.NamespaceSelector == nil && namespace == releaseNamespace {
			return true
		}
		if from.NamespaceSelector != nil && from.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == namespace {
			return true
		}
	}
	return false
}

// The mesh policy admits a caller only if the layer 3/4 policy lets its packets in first.
func TestHelmNetworkPolicy_EveryInternalRouteCallerOfTheMeshPolicyIsAdmittedOnTheHTTPPort(t *testing.T) {
	policy := renderPolicy(t, "wf")
	netPolicy := renderNetworkPolicy(t, "wf")
	require.Greater(t, len(policy.Spec.Rules), 1)

	checked := 0
	for _, rule := range policy.Spec.Rules[1:] {
		for _, from := range rule.From {
			for _, principal := range from.Source.Principals {
				parts := strings.Split(principal, "/")
				require.Len(t, parts, 5, principal)
				namespace, serviceAccount := parts[2], parts[4]
				pods, known := callerPods[serviceAccount]
				require.True(t, known, "%s is a caller of the mesh policy: add it to callerPods and to networkPolicy.allowedCallers", principal)
				assert.Equal(t, pods.namespace, namespace, principal)

				admitted := false
				for _, r := range netPolicy.Spec.Ingress {
					admitted = admitted || admitsPod(r, pods.namespace, pods.name, 8080)
				}
				assert.True(t, admitted, "%s is admitted by the mesh policy but blocked by the NetworkPolicy", principal)
				checked++
			}
		}
	}
	assert.GreaterOrEqual(t, checked, 5, "every caller of every internal route group was checked")
}

func entitlementsEgress(np networkPolicy) []netRule {
	var rules []netRule
	for _, r := range np.Spec.Egress {
		for _, to := range r.To {
			if to.NamespaceSelector != nil && to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == "commercial" {
				rules = append(rules, r)
			}
		}
	}
	return rules
}

func TestHelmEntitlements_TheEgressRuleIsRenderedOnlyWhenTheAddressPortsAndPodsAreSet(t *testing.T) {
	const address = "env.ENTITLEMENTS_BASE_URL=http://entitlements.commercial:8080"

	assert.Empty(t, entitlementsEgress(renderNetworkPolicy(t, "wf")), "no address, no rule")

	rules := entitlementsEgress(renderNetworkPolicy(t, "wf", "--set-string", address))
	require.Len(t, rules, 1)
	require.Len(t, rules[0].To, 1)
	assert.Equal(t, map[string]string{"app.kubernetes.io/name": "commercial-entitlements"}, rules[0].To[0].PodSelector.MatchLabels)
	require.Len(t, rules[0].Ports, 1)
	assert.Equal(t, 8080, rules[0].Ports[0].Port)

	noPorts := writeValues(t, "networkPolicy:\n  entitlementsEgress:\n    ports: []\n")
	assert.Empty(t, entitlementsEgress(renderNetworkPolicy(t, "wf", "--set-string", address, "-f", noPorts)),
		"without ports the rule would admit every port")

	noPods := writeValues(t, "networkPolicy:\n  entitlementsEgress:\n    podSelector: null\n")
	assert.Empty(t, entitlementsEgress(renderNetworkPolicy(t, "wf", "--set-string", address, "-f", noPods)),
		"without a pod selector the rule would admit every pod in the namespace")
}

// route is one internal route read from the router source.
type route struct {
	method string
	path   string // full path, with each :param replaced by a sample segment
}

var internalRouteLine = regexp.MustCompile(`internal\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"`)

func routerInternalRoutes(t *testing.T) []route {
	t.Helper()
	body, err := os.ReadFile(routerFile)
	require.NoError(t, err)

	var routes []route
	for _, m := range internalRouteLine.FindAllStringSubmatch(string(body), -1) {
		segments := strings.Split(m[2], "/")
		for i, s := range segments {
			if strings.HasPrefix(s, ":") {
				segments[i] = "sample"
			}
		}
		routes = append(routes, route{method: m[1], path: internalPrefix + strings.Join(segments, "/")})
	}
	return routes
}

// istioPathMatches follows Istio's path matching: exact, or one "*" at the start or the end. A
// pattern "/x/*" matches paths below "/x/" and never "/x" itself.
func istioPathMatches(pattern, path string) bool {
	switch {
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	case strings.HasPrefix(pattern, "*"):
		return strings.HasSuffix(path, strings.TrimPrefix(pattern, "*"))
	default:
		return path == pattern
	}
}

// groupsAdmitting names the rules of the policy (after rule 1) whose paths match the route.
func groupsAdmitting(rules []policyRule, want []wantRule, r route) []string {
	var names []string
	for i, rule := range rules[1:] {
		for _, to := range rule.To {
			for _, p := range to.Operation.Paths {
				if istioPathMatches(p, r.path) {
					names = append(names, want[i].name)
				}
			}
		}
	}
	return names
}

// A route added to router.go with no caller list must fail here, not be silently open or closed.
func TestHelmAuthorizationPolicy_EveryInternalRouteBelongsToExactlyOneGroup(t *testing.T) {
	routes := routerInternalRoutes(t)
	require.NotEmpty(t, routes, "no internal route was read from the router, so the check proved nothing")

	p := renderPolicy(t, "wf")
	want := defaultInternalRules()
	require.Empty(t, policyProblems(p.Spec.Rules, want))

	expectedGroup := map[string]string{
		"POST " + internalPrefix + "/events":                                "events",
		"GET " + internalPrefix + "/workflow-versions/sample/compiled-plan": "compiled-plans",
		"GET " + internalPrefix + "/connector-aliases":                      "connector-alias-read",
		"POST " + internalPrefix + "/connector-aliases/rest":                "connector-alias-write",
		"DELETE " + internalPrefix + "/connector-aliases/rest/sample":       "connector-alias-write",
	}

	seen := map[string]bool{}
	for _, r := range routes {
		key := r.method + " " + r.path
		seen[key] = true
		groups := groupsAdmitting(p.Spec.Rules, want, r)
		assert.Len(t, groups, 1, "%s must match exactly one caller group, matched %v: add a group to authorizationPolicy.internalRoutes", key, groups)
		if len(groups) == 1 {
			assert.Equal(t, expectedGroup[key], groups[0], key)
		}
	}
	for key := range expectedGroup {
		assert.True(t, seen[key], "%s is expected but the router no longer lists it", key)
	}
}

func TestHelmAuthorizationPolicy_TheRouteCheckSeesAnUncoveredRouteAndAnOverlap(t *testing.T) {
	p := renderPolicy(t, "wf")
	want := defaultInternalRules()

	assert.Empty(t, groupsAdmitting(p.Spec.Rules, want, route{"POST", internalPrefix + "/tenants/purge"}), "a new route no group names")
	assert.Len(t, groupsAdmitting(p.Spec.Rules, want, route{"GET", internalPrefix + "/connector-aliases/rest"}), 1)
	assert.Len(t, groupsAdmitting(p.Spec.Rules, want, route{"GET", internalPrefix + "/connector-aliases"}), 1,
		"the exact list path belongs to the read group only: /connector-aliases/* does not match it")

	overlapping := append([]policyRule(nil), p.Spec.Rules...)
	overlapping = append(overlapping, overlapping[1])
	overlappingWant := append(append([]wantRule(nil), want...), want[0])
	assert.Len(t, groupsAdmitting(overlapping, overlappingWant, route{"POST", internalPrefix + "/events"}), 2)
}

// The removed shared-token settings are gone: no template reads them and the values do not carry them.
func TestHelmChart_CarriesNoRemovedTokenName(t *testing.T) {
	var files, matches []string
	removed := []string{"INTERNAL_API_TOKEN", "EXECUTION_SERVICE_TOKEN"}

	err := filepath.WalkDir(chartDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files = append(files, path)
		for _, name := range removed {
			if bytes.Contains(body, []byte(name)) {
				matches = append(matches, path+" names "+name)
			}
		}
		return nil
	})

	require.NoError(t, err)
	require.NotEmpty(t, files, "the chart directory was not found, so the check proved nothing")
	assert.Contains(t, files, filepath.Join(chartDir, "values.yaml"))
	assert.Empty(t, matches)
}
