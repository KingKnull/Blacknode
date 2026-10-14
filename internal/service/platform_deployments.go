package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type DeploymentProject struct {
	ID string `json:"id"`
	Name string `json:"name"`
	HostID string `json:"hostID"`
	Directory string `json:"directory"`
	Tool string `json:"tool"`
	WorkspaceID string `json:"workspaceID"`
	Playbook string `json:"playbook"`
	Inventory string `json:"inventory"`
	IntegrationID string `json:"integrationID"`
	Repository string `json:"repository"`
}

func (s *PlatformService) SaveProject(ctx context.Context,p DeploymentProject,revision int64)(store.OperationDocument,error){
	if p.Name=="" || len(p.Name)>120 || p.Directory=="" || len(p.Directory)>4096 || strings.ContainsAny(p.Directory,"\x00\n\r"){return store.OperationDocument{},errors.New("project name and working directory required")}
	if p.Tool!="terraform" && p.Tool!="tofu" && p.Tool!="ansible"{return store.OperationDocument{},errors.New("choose Terraform, OpenTofu or Ansible")}
	if p.HostID!="" {if _,err:=s.hosts.GetResolved(p.HostID);err!=nil{return store.OperationDocument{},err}}
	if p.Tool=="ansible" && (p.Playbook=="" || p.Inventory==""){return store.OperationDocument{},errors.New("playbook and inventory paths required")}
	if len(p.Playbook)>4096 || len(p.Inventory)>4096{return store.OperationDocument{},errors.New("project path too long")}
	if p.ID==""{p.ID=uuid.NewString()};return s.store.Put("project",p.ID,revision,p)
}

func (s *PlatformService) PreviewProjectInit(ctx context.Context,id string)(ChangePlan,error){
	var p DeploymentProject;if err:=s.store.Read("project",id,&p);err!=nil{return ChangePlan{},err};if p.Tool!="terraform" && p.Tool!="tofu"{return ChangePlan{},errors.New("initialization applies to Terraform and OpenTofu projects")}
	return s.prepare(p.HostID,p.WorkspaceID,"Initialize "+p.Name,"cd -- "+sshconn.ShellEscape(p.Directory)+" && "+p.Tool+" init -input=false","","",600,nil)
}

func (s *PlatformService) PreviewDeployment(ctx context.Context,id string)(ChangePlan,error){
	var p DeploymentProject;if err:=s.store.Read("project",id,&p);err!=nil{return ChangePlan{},err}
	prefix:="cd -- "+sshconn.ShellEscape(p.Directory)+" && "
	if p.Tool=="ansible" {
		cmd:=prefix+"ansible-playbook "+sshconn.ShellEscape(p.Playbook)+" --inventory "+sshconn.ShellEscape(p.Inventory)
		preview,err:=commandOutput(s.command(ctx,p.HostID,cmd+" --check --diff","",600));if err!=nil{return ChangePlan{},err}
		return s.prepare(p.HostID,p.WorkspaceID,"Run "+p.Name,cmd,"",redactEvidence(preview),3600,nil)
	}
	if p.Tool!="terraform" && p.Tool!="tofu"{return ChangePlan{},errors.New("unsupported deployment tool")}
	artifact:=".blacknode-"+uuid.NewString()+".tfplan";file:=sshconn.ShellEscape(artifact)
	cleanup:=func(ctx context.Context){s.command(ctx,p.HostID,prefix+"rm -f -- "+file,"",10)}
	command:="umask 077; "+prefix+p.Tool+" plan -input=false -lock-timeout=30s -out="+file+" >/dev/null && "+p.Tool+" show -json "+file
	output,err:=commandOutput(s.command(ctx,p.HostID,command,"",600));if err!=nil{cleanup(ctx);return ChangePlan{},err}
	summary,err:=summarizeTerraform([]byte(output));if err!=nil{cleanup(ctx);return ChangePlan{},err}
	digest,err:=commandOutput(s.command(ctx,p.HostID,prefix+"sha256sum "+file,"",15));if err!=nil{cleanup(ctx);return ChangePlan{},err}
	verify:=func(ctx context.Context)error{next,err:=commandOutput(s.command(ctx,p.HostID,prefix+"sha256sum "+file,"",15));if err!=nil{return err};if next!=digest{return errors.New("saved plan changed; generate a fresh plan")};return nil}
	plan,err:=s.prepare(p.HostID,p.WorkspaceID,"Apply "+p.Name,prefix+p.Tool+" apply -input=false "+file,"",summary,3600,verify)
	if err!=nil{cleanup(ctx);return plan,err};s.mu.Lock();change:=s.plans[plan.ID];change.cleanup=cleanup;s.plans[plan.ID]=change;s.mu.Unlock();return plan,nil
}

func summarizeTerraform(data []byte)(string,error){
	var plan struct{Changes []struct{Address string `json:"address"`;Change struct{Actions []string `json:"actions"`;Before any `json:"before"`;After any `json:"after"`;BeforeSensitive any `json:"before_sensitive"`;AfterSensitive any `json:"after_sensitive"`} `json:"change"`} `json:"resource_changes"`}
	if err:=json.Unmarshal(data,&plan);err!=nil{return "",fmt.Errorf("parse infrastructure plan: %w",err)}
	out:=[]map[string]any{};for _,change:=range plan.Changes{if len(change.Change.Actions)==1 && change.Change.Actions[0]=="no-op"{continue};out=append(out,map[string]any{"resource":change.Address,"actions":change.Change.Actions,"before":maskSensitive(change.Change.Before,change.Change.BeforeSensitive),"after":maskSensitive(change.Change.After,change.Change.AfterSensitive)})}
	pretty,err:=json.MarshalIndent(out,"","  ");return string(pretty),err
}

func maskSensitive(value,mask any)any{
	if secret,ok:=mask.(bool);ok && secret{return "[SENSITIVE]"}
	switch v:=value.(type){case map[string]any:m,_:=mask.(map[string]any);out:=map[string]any{};for key,item:=range v{out[key]=maskSensitive(item,m[key])};return out
	case []any:m,_:=mask.([]any);out:=make([]any,len(v));for i,item:=range v{var sub any;if i<len(m){sub=m[i]};out[i]=maskSensitive(item,sub)};return out
	case string:return redactEvidence(v)};return value
}

type PipelineRun struct{ID string `json:"id"`;Name string `json:"name"`;Status string `json:"status"`;Conclusion string `json:"conclusion"`;Commit string `json:"commit"`;Branch string `json:"branch"`;URL string `json:"url"`;CreatedAt string `json:"createdAt"`}
var repositoryName=regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+$`)

func (s *PlatformService) PipelineRuns(ctx context.Context,integrationID,repository string)([]PipelineRun,error){
	if !repositoryName.MatchString(repository) || len(repository)>512{return nil,errors.New("use a repository path such as owner/project")}
	var config Integration;if err:=s.store.Read("integration",integrationID,&config);err!=nil{return nil,err}
	resource:="";switch config.Kind{case "github":if strings.Count(repository,"/")!=1{return nil,errors.New("GitHub repositories use owner/project")};resource="repos/"+repository+"/actions/runs";case "gitlab":resource="projects/"+url.PathEscape(repository)+"/pipelines";default:return nil,errors.New("select a GitHub or GitLab integration")}
	data,err:=s.integrationRequest(ctx,config,"GET",resource,url.Values{"per_page":{"30"}},nil);if err!=nil{return nil,err};out:=[]PipelineRun{}
	if config.Kind=="github"{var list struct{Runs []struct{ID int64 `json:"id"`;Name string `json:"name"`;Status string `json:"status"`;Conclusion string `json:"conclusion"`;SHA string `json:"head_sha"`;Branch string `json:"head_branch"`;URL string `json:"html_url"`;Created string `json:"created_at"`} `json:"workflow_runs"`};if err=json.Unmarshal(data,&list);err!=nil{return nil,err};for _,r:=range list.Runs{out=append(out,PipelineRun{ID:strconv.FormatInt(r.ID,10),Name:r.Name,Status:r.Status,Conclusion:r.Conclusion,Commit:r.SHA,Branch:r.Branch,URL:r.URL,CreatedAt:r.Created})}
	}else{var list []struct{ID int64 `json:"id"`;Status string `json:"status"`;SHA string `json:"sha"`;Branch string `json:"ref"`;URL string `json:"web_url"`;Created string `json:"created_at"`};if err=json.Unmarshal(data,&list);err!=nil{return nil,err};for _,r:=range list{out=append(out,PipelineRun{ID:strconv.FormatInt(r.ID,10),Name:repository,Status:r.Status,Commit:r.SHA,Branch:r.Branch,URL:r.URL,CreatedAt:r.Created})}}
	return out,nil
}

func (s *PlatformService) PinPipeline(ctx context.Context,workspaceID,integrationID,repository,runID string)(store.OperationEvent,error){
	runs,err:=s.PipelineRuns(ctx,integrationID,repository);if err!=nil{return store.OperationEvent{},err};for _,run:=range runs{if run.ID==runID{return s.store.Append(workspaceID,"deployment.pipeline",run.Name+" · "+run.Status,run)}};return store.OperationEvent{},errors.New("pipeline run not found")
}
