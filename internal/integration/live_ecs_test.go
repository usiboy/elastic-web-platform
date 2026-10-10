package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	urlpkg "net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fixture struct { label,team,project,host,indexDigest,ownerFile string }

func protectedToken(t *testing.T,file string)string {
	t.Helper()
	info,err:=os.Lstat(file)
	if err!=nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077!=0 { t.Fatal("owner identity file must be a private regular file") }
	data,err:=os.ReadFile(file)
	if err!=nil { t.Fatal("owner identity unavailable") }
	return strings.TrimSpace(string(data))
}

func TestLiveECSPrivateABHostsWithVerifiedTLSAndNoLegacyRegression(t *testing.T) {
	ip:=os.Getenv("EWP_LIVE_ECS_IP")
	if ip=="" { t.Skip("set explicit EWP_LIVE_ECS_IP and protected fixture identities for real ECS verification") }
	if ip!="114.55.237.62" || net.ParseIP(ip)==nil { t.Fatal("wrong ECS destination") }
	caFile:=os.Getenv("EWP_LIVE_ECS_CA_FILE")
	caBytes,err:=os.ReadFile(caFile)
	if err!=nil { t.Fatal("private test CA unavailable") }
	trust:=x509.NewCertPool()
	if !trust.AppendCertsFromPEM(caBytes) { t.Fatal("invalid test CA") }
	transport:=&http.Transport{
		Proxy:nil,
		TLSClientConfig:&tls.Config{RootCAs:trust,MinVersion:tls.VersionTLS12},
		DialContext:func(ctx context.Context,network,_ string)(net.Conn,error){ return (&net.Dialer{Timeout:5*time.Second}).DialContext(ctx,network,net.JoinHostPort(ip,"443")) },
	}
	client:=&http.Client{Transport:transport,Timeout:12*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	requests:=func(method,url,token string,body []byte,origin string,cookie *http.Cookie)(*http.Response,[]byte,error) {
		req,err:=http.NewRequest(method,url,bytes.NewReader(body))
		if err!=nil { return nil,nil,err }
		if token!="" { req.Header.Set("Authorization","Bearer "+token) }
		if origin!="" { req.Header.Set("Origin",origin); req.Header.Set("Content-Type","application/x-www-form-urlencoded") }
		if cookie!=nil { req.AddCookie(cookie) }
		resp,err:=client.Do(req)
		if err!=nil { return nil,nil,err }
		data,err:=io.ReadAll(io.LimitReader(resp.Body,(1<<20)+1))
		_ = resp.Body.Close()
		if err!=nil || len(data)>1<<20 { return nil,nil,errors.New("ECS response too large") }
		return resp,data,nil
	}
	fixtures:=[]fixture{
		{"A",os.Getenv("EWP_LIVE_ECS_TEAM_A"),os.Getenv("EWP_LIVE_ECS_PROJECT_A"),"a.preview.ecp.lingtong.com","3428587748ce2e568eff8e22ab8db33f20061df832180b915aa52778bbeb9419",os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE")},
		{"B",os.Getenv("EWP_LIVE_ECS_TEAM_B"),os.Getenv("EWP_LIVE_ECS_PROJECT_B"),"b.preview.ecp.lingtong.com","b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a",os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE")},
	}
	var cookies [2]*http.Cookie
	for index,item:=range fixtures {
		if item.team=="" || item.project=="" || item.ownerFile=="" { t.Fatal("both independent Team/Project/Owner scopes required") }
		owner:=protectedToken(t,item.ownerFile)
		me,body,err:=requests(http.MethodGet,"https://console.ecp.lingtong.com/v1/me",owner,nil,"",nil)
		if err!=nil || me.StatusCode!=200 { t.Fatalf("%s scoped API identity failed: %v",item.label,err) }
		var actor struct { TeamID string `json:"teamId"`; Role string `json:"role"` }
		if err:=json.Unmarshal(body,&actor); err!=nil || actor.TeamID!=item.team || actor.Role!="owner" { t.Fatalf("%s identity mapping mismatch: %v",item.label,err) }
		other:=fixtures[1-index]
		cross,_,err:=requests(http.MethodGet,fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s",other.team,other.project),owner,nil,"",nil)
		if err!=nil || cross.StatusCode!=404 { t.Fatalf("%s crossed Team resources: %v",item.label,err) }
		resources,resourceBody,err:=requests(http.MethodGet,fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/resources",item.team,item.project),owner,nil,"",nil)
		if err!=nil || resources.StatusCode!=200 { t.Fatalf("%s resource reservation readback unavailable: %v",item.label,err) }
		var allocation struct { TeamID string `json:"teamId"`; ProjectID string `json:"projectId"`; State string `json:"state"`; Resources struct { CPUMilli int64 `json:"cpuMilli"`; MemoryMiB int64 `json:"memoryMiB"`; StorageMiB int64 `json:"storageMiB"` } `json:"resources"` }
		if err:=json.Unmarshal(resourceBody,&allocation); err!=nil || allocation.TeamID!=item.team || allocation.ProjectID!=item.project || (allocation.State!="unallocated" && allocation.State!="reserved_not_provisioned") { t.Fatalf("%s resource control state invalid: %v",item.label,err) }
		if os.Getenv("EWP_LIVE_ECS_VERIFY_RESERVATIONS")=="1" {
			expectedCPU,expectedMemory,expectedStorage:=int64(1000),int64(2048),int64(2048)
			if item.label=="B" {
				expectedCPU,expectedMemory,expectedStorage=500,512,256
				if os.Getenv("EWP_LIVE_ECS_VERIFY_BUILDER_BUDGET")=="1" { expectedCPU,expectedMemory,expectedStorage=2000,3072,2048 }
			}
			if allocation.State!="reserved_not_provisioned" || allocation.Resources.CPUMilli!=expectedCPU || allocation.Resources.MemoryMiB!=expectedMemory || allocation.Resources.StorageMiB!=expectedStorage { t.Fatalf("%s resource reservation differs from tested budget",item.label) }
			teamBudget,teamBody,budgetErr:=requests(http.MethodGet,fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/resources",item.team),owner,nil,"",nil)
			if budgetErr!=nil || teamBudget.StatusCode!=200 { t.Fatalf("%s team resource readback unavailable: %v",item.label,budgetErr) }
			var summary struct { State string `json:"state"`; Reserved struct { CPUMilli int64 `json:"cpuMilli"`; MemoryMiB int64 `json:"memoryMiB"`; StorageMiB int64 `json:"storageMiB"` } `json:"reserved"` }
			if err:=json.Unmarshal(teamBody,&summary); err!=nil || summary.State!="budgeted_not_provisioned" || summary.Reserved.CPUMilli!=expectedCPU || summary.Reserved.MemoryMiB!=expectedMemory || summary.Reserved.StorageMiB!=expectedStorage { t.Fatalf("%s team budget reservation readback invalid: %v",item.label,err) }
		}
		cross,_,err=requests(http.MethodGet,fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/resources",other.team,other.project),owner,nil,"",nil)
		if err!=nil || cross.StatusCode!=404 { t.Fatalf("%s crossed Team resource reservation: %v",item.label,err) }
		pushURL:=fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/github-pushes",item.team,item.project)
		pushResponse,pushBody,pushErr:=requests(http.MethodGet,pushURL,owner,nil,"",nil)
		if pushErr!=nil || pushResponse.StatusCode!=200 { t.Fatalf("%s Git history unavailable: %v",item.label,pushErr) }
		var pushPage struct { Pushes []struct { Status string `json:"status"`; DeploymentID string `json:"deploymentId"` } `json:"pushes"` }
		if err:=json.Unmarshal(pushBody,&pushPage); err!=nil || len(pushPage.Pushes)>20 { t.Fatalf("%s Git history page invalid: %v",item.label,err) }
		for _,push:=range pushPage.Pushes { if push.Status=="awaiting_isolated_build" && push.DeploymentID!="" { t.Fatalf("%s unbuilt Git source returned deployment",item.label) } }
		cross,_,err=requests(http.MethodGet,fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/github-pushes",other.team,other.project),owner,nil,"",nil)
		if err!=nil || cross.StatusCode!=404 { t.Fatalf("%s crossed Team Git history: %v",item.label,err) }
		url:=fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/deployments",item.team,item.project)
		deploymentsURL:=url
		list,payload,err:=requests(http.MethodGet,url,owner,nil,"",nil)
		if err!=nil || list.StatusCode!=200 { t.Fatalf("%s deployment list unavailable: %v",item.label,err) }
		var entries struct { Deployments []struct{ ID string `json:"deploymentId"`; Host string `json:"desiredHost"`; Status string `json:"observedState"` } `json:"deployments"` }
		if err:=json.Unmarshal(payload,&entries); err!=nil { t.Fatalf("%s deployment list invalid: %v",item.label,err) }
		var dep string
		for _,entry:=range entries.Deployments {
			if entry.Host==item.host {
				if dep!="" || (entry.Status!="not_deployed" && entry.Status!="preview_ready") { t.Fatalf("%s fixed Host ambiguous or invalid: %+v",item.label,entry) }
				dep=entry.ID
			}
		}
		if dep=="" { t.Fatalf("%s fixed Host missing: %s",item.label,item.host) }
		url=fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/deployments/%s/preview-exchanges",item.team,item.project,dep)
		response,payload,err:=requests(http.MethodPost,url,owner,nil,"",nil)
		if err!=nil || response.StatusCode!=201 { t.Fatalf("%s code issue failed: %v",item.label,err) }
		var issued struct { Code string `json:"code"`; Host string `json:"desiredHost"` }
		if err:=json.Unmarshal(payload,&issued); err!=nil || issued.Code=="" || issued.Host!=item.host { t.Fatalf("%s code scope invalid: %v",item.label,err) }
		previewURL:="https://"+item.host+"/"
		anonymous,_,err:=requests(http.MethodGet,previewURL,"",nil,"",nil)
		if err!=nil || anonymous.StatusCode!=401 { t.Fatalf("%s anonymous preview not protected: %v",item.label,err) }
		form:=[]byte("code="+urlpkg.QueryEscape(issued.Code))
		exchange,_,err:=requests(http.MethodPost,"https://"+item.host+"/session/exchange","",form,"https://console.ecp.lingtong.com",nil)
		if err!=nil || exchange.StatusCode!=303 || len(exchange.Cookies())!=1 || !exchange.Cookies()[0].Secure || !exchange.Cookies()[0].HttpOnly || exchange.Cookies()[0].Domain!="" { t.Fatalf("%s private session not protected: %v",item.label,err) }
		cookies[index]=exchange.Cookies()[0]
		page,html,err:=requests(http.MethodGet,previewURL,"",nil,"",cookies[index])
		if err!=nil || page.StatusCode!=200 || page.Header.Get("Cache-Control")!="no-store" { t.Fatalf("%s HTML Preview invalid: %v",item.label,err) }
		hash:=sha256.Sum256(html)
		if hex.EncodeToString(hash[:])!=item.indexDigest { t.Fatalf("%s served wrong source-backed HTML digest",item.label) }
		pattern:=regexp.MustCompile(`(?:src|href)="(/assets/[^\"]+\.js)"`)
		match:=pattern.FindSubmatch(html)
		if len(match)<2 { t.Fatalf("%s missing JS asset",item.label) }
		jsResponse,js,err:=requests(http.MethodGet,"https://"+item.host+string(match[1]),"",nil,"",cookies[index])
		if err!=nil || jsResponse.StatusCode!=200 || len(js)<100 { t.Fatalf("%s JS resource failed: %v",item.label,err) }
		observed,observedPayload,err:=requests(http.MethodGet,deploymentsURL,owner,nil,"",nil)
		if err!=nil || observed.StatusCode!=200 { t.Fatalf("%s observed deployment unavailable: %v",item.label,err) }
		if err:=json.Unmarshal(observedPayload,&entries); err!=nil { t.Fatalf("%s observed deployment list invalid: %v",item.label,err) }
		var ready bool
		for _,entry:=range entries.Deployments { if entry.ID==dep && entry.Host==item.host && entry.Status=="preview_ready" { ready=true } }
		if !ready { t.Fatalf("%s gateway readback not observed for %s",item.label,dep) }
		t.Logf("tenant=%s host=%s TLS=verified HTML_SHA256=%s JS_bytes=%d",item.label,item.host,item.indexDigest,len(js))
	}
	wrong,_,err:=requests(http.MethodGet,"https://"+fixtures[1].host+"/","",nil,"",cookies[0])
	if err!=nil || wrong.StatusCode!=401 { t.Fatalf("A Preview cookie crossed to B: %v",err) }
	// Old IP's publicly trusted certificate and anonymous 401 are a separate
	// regression guard checked with the host system roots (not the test CA).
	legacy:=&http.Client{Timeout:8*time.Second,Transport:&http.Transport{Proxy:nil}}
	response,err:=legacy.Get("https://"+ip+"/")
	if err!=nil { t.Fatal("legacy IP TLS regressed: ",err) }
	_ = response.Body.Close()
	if response.StatusCode!=401 { t.Fatalf("legacy site status drifted: %d",response.StatusCode) }
}
