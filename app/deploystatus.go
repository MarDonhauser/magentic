package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"magentic/core"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const azureDevOpsResourceID = "499b84ac-1321-427f-aa17-267ca6975798"

type BuildInfo struct {
	Repo     string `json:"repo"`
	Status   string `json:"status"`
	Result   string `json:"result"`
	Branch   string `json:"branch"`
	Age      string `json:"age"`
	URL      string `json:"url"`
}

type ArgoApp struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Sync      string `json:"sync"`
	Health    string `json:"health"`
	URL       string `json:"url"`
}

func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func projectKeywords() []string {
	st, err := core.LoadState()
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range st.Projects {
		if n := normName(p.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

type DeployStatus struct {
	AzOK       bool        `json:"azOk"`
	AzErr      string      `json:"azErr"`
	AzSub      string      `json:"azSub"`
	AzSubID    string      `json:"azSubId"`
	ArgoOK     bool        `json:"argoOk"`
	ArgoServer string      `json:"argoServer"`
	ArgoErr    string      `json:"argoErr"`
	Builds     []BuildInfo `json:"builds"`
	Apps       []ArgoApp   `json:"apps"`
}

type AzAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

var azdoRemoteRe = regexp.MustCompile(`dev\.azure\.com[:/](?:v3/)?([^/@]+)/([^/]+)(?:/_git)?/`)

func azdoOrgProjects() [][2]string {
	st, err := core.LoadState()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out [][2]string
	for _, p := range st.Projects {
		url, err := core.GitCmd(p.Path, "remote", "get-url", "origin")
		if err != nil {
			continue
		}
		m := azdoRemoteRe.FindStringSubmatch(strings.TrimSpace(url))
		if m == nil {
			continue
		}
		org, proj := m[1], m[2]
		if strings.Contains(org, "@") {
			org = org[strings.Index(org, "@")+1:]
		}
		key := org + "/" + proj
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, [2]string{org, proj})
	}
	return out
}

func argoServer() string {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.config/argocd/config")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "current-context:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "current-context:"))
		}
	}
	return ""
}

func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg := strings.TrimSpace(string(ee.Stderr))
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return out, nil
}

func (a *App) DeployStatus() DeployStatus {
	ds := DeployStatus{}
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := exec.LookPath("az"); err != nil {
			ds.AzErr = "az CLI nicht installiert"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "az", "account", "show", "-o", "json"); err == nil {
			var acc struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(out, &acc) == nil {
				ds.AzSub = acc.Name
				ds.AzSubID = acc.ID
			}
		}
		pairs := azdoOrgProjects()
		if len(pairs) == 0 {
			ds.AzErr = "kein Azure-DevOps-Remote in den Projekten gefunden"
			return
		}
		for _, pair := range pairs {
			org, proj := pair[0], pair[1]
			out, err := runCmd(ctx, "az", "pipelines", "runs", "list",
				"--organization", "https://dev.azure.com/"+org,
				"--project", proj, "--top", "8", "-o", "json")
			if err != nil {
				ds.AzErr = shortLoginErr(err.Error())
				return
			}
			var runs []struct {
				ID         int    `json:"id"`
				Status     string `json:"status"`
				Result     string `json:"result"`
				SourceBranch string `json:"sourceBranch"`
				FinishTime string `json:"finishTime"`
				StartTime  string `json:"startTime"`
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			}
			if err := json.Unmarshal(out, &runs); err != nil {
				ds.AzErr = err.Error()
				return
			}
			for _, r := range runs {
				ts := r.FinishTime
				if ts == "" {
					ts = r.StartTime
				}
				age := ""
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					age = core.FormatAgeWord(t.Local())
				}
				branch := strings.TrimPrefix(strings.TrimPrefix(r.SourceBranch, "refs/heads/"), "refs/tags/")
				ds.Builds = append(ds.Builds, BuildInfo{
					Repo:   r.Definition.Name,
					Status: r.Status,
					Result: r.Result,
					Branch: branch,
					Age:    age,
					URL:    fmt.Sprintf("https://dev.azure.com/%s/%s/_build/results?buildId=%d", org, proj, r.ID),
				})
			}
		}
		ds.AzOK = true
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := exec.LookPath("argocd"); err != nil {
			ds.ArgoErr = "argocd CLI nicht installiert"
			return
		}
		ds.ArgoServer = argoServer()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := runCmd(ctx, "argocd", "app", "list", "-o", "json")
		if err != nil {
			ds.ArgoErr = shortLoginErr(err.Error())
			return
		}
		var apps []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Destination struct {
					Namespace string `json:"namespace"`
				} `json:"destination"`
			} `json:"spec"`
			Status struct {
				Sync struct {
					Status string `json:"status"`
				} `json:"sync"`
				Health struct {
					Status string `json:"status"`
				} `json:"health"`
			} `json:"status"`
		}
		if err := json.Unmarshal(out, &apps); err != nil {
			ds.ArgoErr = err.Error()
			return
		}
		keywords := projectKeywords()
		nsSet := map[string]bool{}
		for _, ap := range apps {
			n := normName(ap.Metadata.Name)
			for _, kw := range keywords {
				if strings.Contains(n, kw) {
					nsSet[ap.Spec.Destination.Namespace] = true
				}
			}
		}
		for _, ap := range apps {
			if len(nsSet) > 0 && !nsSet[ap.Spec.Destination.Namespace] {
				continue
			}
			ds.Apps = append(ds.Apps, ArgoApp{
				Name:      ap.Metadata.Name,
				Namespace: ap.Spec.Destination.Namespace,
				Sync:      ap.Status.Sync.Status,
				Health:    ap.Status.Health.Status,
				URL:       "https://" + ds.ArgoServer + "/applications/" + ap.Metadata.Name,
			})
		}
		sort.SliceStable(ds.Apps, func(i, j int) bool {
			return argoRank(ds.Apps[i]) < argoRank(ds.Apps[j])
		})
		ds.ArgoOK = true
	}()

	wg.Wait()

	a.dsMu.Lock()
	prev := a.dsPrev
	snapshot := ds
	a.dsPrev = &snapshot
	a.dsMu.Unlock()
	if prev != nil {
		notifyDeployTransitions(prev, &ds)
	}
	return ds
}

func notifyDeployTransitions(prev, cur *DeployStatus) {
	prevBuild := map[string]BuildInfo{}
	for _, b := range prev.Builds {
		prevBuild[b.URL] = b
	}
	for _, b := range cur.Builds {
		pb, known := prevBuild[b.URL]
		if b.Status == "completed" && b.Result == "failed" && (!known || pb.Status != "completed") {
			core.NotifyDesktop("magentic · Build failed", b.Repo+" ("+b.Branch+")", "Basso")
		}
		if b.Status == "completed" && b.Result == "succeeded" && known && pb.Status == "inProgress" {
			core.NotifyDesktop("magentic · Build fertig", b.Repo+" ✓ ("+b.Branch+")", "Ping")
		}
	}
	prevApp := map[string]ArgoApp{}
	for _, ap := range prev.Apps {
		prevApp[ap.Name] = ap
	}
	for _, ap := range cur.Apps {
		pa, known := prevApp[ap.Name]
		if !known {
			continue
		}
		if pa.Health != "Degraded" && ap.Health == "Degraded" {
			core.NotifyDesktop("magentic · Argo Degraded", ap.Name, "Basso")
		}
		if pa.Health == "Progressing" && ap.Health == "Healthy" {
			core.NotifyDesktop("magentic · Argo Healthy", ap.Name+" ✓", "Ping")
		}
	}
}

func argoRank(a ArgoApp) int {
	if a.Health == "Degraded" || a.Health == "Missing" {
		return 0
	}
	if a.Health == "Progressing" {
		return 1
	}
	if a.Sync != "Synced" {
		return 2
	}
	return 3
}

func shortLoginErr(msg string) string {
	low := strings.ToLower(msg)
	if strings.Contains(low, "az login") || strings.Contains(low, "aadsts") || strings.Contains(low, "refresh token") || strings.Contains(low, "authentication") {
		return "nicht angemeldet — az login nötig"
	}
	if strings.Contains(low, "token is expired") || strings.Contains(low, "unauthenticated") || strings.Contains(low, "invalid session") || strings.Contains(low, "failed to get user info") {
		return "nicht angemeldet — argocd login nötig"
	}
	return msg
}

func (a *App) AzAccounts() []AzAccount {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := runCmd(ctx, "az", "account", "list", "--all", "-o", "json")
	if err != nil {
		return []AzAccount{}
	}
	var raw []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	}
	if json.Unmarshal(out, &raw) != nil {
		return []AzAccount{}
	}
	accs := []AzAccount{}
	for _, r := range raw {
		accs = append(accs, AzAccount{ID: r.ID, Name: r.Name, IsDefault: r.IsDefault})
	}
	return accs
}

func (a *App) AzSetSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("keine Subscription angegeben")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := runCmd(ctx, "az", "account", "set", "--subscription", id); err != nil {
		return fmt.Errorf("%s", shortLoginErr(err.Error()))
	}
	return nil
}

func (a *App) AzLogin() {
	go func() {
		args := []string{"login", "--scope", azureDevOpsResourceID + "/.default"}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if out, err := runCmd(ctx, "az", "account", "show", "--query", "tenantId", "-o", "tsv"); err == nil {
			if tenant := strings.TrimSpace(string(out)); tenant != "" {
				args = append(args, "--tenant", tenant)
			}
		}
		cancel()
		cmd := exec.Command("az", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if len(msg) > 200 {
				msg = msg[:200]
			}
			runtime.EventsEmit(a.ctx, "login:az", "Fehler: "+msg)
			return
		}
		runtime.EventsEmit(a.ctx, "login:az", "ok")
	}()
}

func (a *App) ArgoLogin() {
	go func() {
		server := argoServer()
		if server == "" {
			runtime.EventsEmit(a.ctx, "login:argo", "Fehler: kein argocd-Kontext gefunden")
			return
		}
		cmd := exec.Command("argocd", "login", server, "--sso", "--grpc-web")
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if len(msg) > 200 {
				msg = msg[:200]
			}
			runtime.EventsEmit(a.ctx, "login:argo", "Fehler: "+msg)
			return
		}
		runtime.EventsEmit(a.ctx, "login:argo", "ok")
	}()
}
