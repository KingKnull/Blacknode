package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/google/uuid"
)

type KubeForward struct{ID string `json:"id"`;Target KubeTarget `json:"target"`;Resource string `json:"resource"`;LocalPort int `json:"localPort"`;TargetPort int `json:"targetPort"`;Status string `json:"status"`;Error string `json:"error"`}
type liveKubeForward struct{state KubeForward;cancel context.CancelFunc}
var forwardAddress=regexp.MustCompile(`Forwarding from 127\.0\.0\.1:([0-9]+)`)

func (s *PlatformService) KubeForwards(ctx context.Context)[]KubeForward{s.mu.Lock();defer s.mu.Unlock();out:=[]KubeForward{};for _,f:=range s.kubeForwards{out=append(out,f.state)};return out}
func (s *PlatformService) StopKubeForward(ctx context.Context,id string){s.mu.Lock();f:=s.kubeForwards[id];delete(s.kubeForwards,id);s.mu.Unlock();if f!=nil{f.cancel()}}
func (s *PlatformService) StopKubeForwards(){s.mu.Lock();defer s.mu.Unlock();for _,f:=range s.kubeForwards{f.cancel()};s.kubeForwards=map[string]*liveKubeForward{}}

func (s *PlatformService) StartKubeForward(ctx context.Context,target KubeTarget,kind,name string,localPort,targetPort int)(KubeForward,error){
	state:=KubeForward{ID:uuid.NewString(),Target:target,Resource:kind+"/"+name,LocalPort:localPort,TargetPort:targetPort,Status:"starting"}
	if (kind!="pods" && kind!="services" && kind!="deployments") || !kubeName.MatchString(name) || localPort<1 || localPort>65535 || targetPort<1 || targetPort>65535{return state,errors.New("select a pod, service or deployment and valid ports")}
	base,err:=kubeBase(target);if err!=nil{return state,err};runCtx,cancel:=context.WithCancel(context.Background())
	reader,writer:=io.Pipe();cleanup:=func(){cancel();reader.Close();writer.Close()}
	ready:=make(chan int,1);finished:=make(chan error,1)
	command:=base+" port-forward --address=127.0.0.1 "+sshconn.ShellEscape(state.Resource)+" "
	var listener net.Listener
	var wait func()error
	var dial func(int)(net.Conn,error)
	var release func()
	if target.HostID=="" {
		command+=fmt.Sprintf("%d:%d",localPort,targetPort);cmd:=exec.CommandContext(runCtx,"sh","-c",command);cmd.Stdout=writer;cmd.Stderr=writer;cmd.WaitDelay=2*time.Second
		if err=cmd.Start();err!=nil{cleanup();return state,err};wait=cmd.Wait;release=func(){}
	} else {
		h,err:=s.hosts.GetResolved(target.HostID);if err!=nil{cleanup();return state,err};client,rel,err:=s.pool.Get(sshconn.FromHost(h));if err!=nil{cleanup();return state,err};release=rel
		session,err:=client.NewSession();if err!=nil{release();cleanup();return state,err};session.Stdout=writer;session.Stderr=writer
		command+=fmt.Sprintf("0:%d",targetPort);if err=session.Start(command);err!=nil{session.Close();release();cleanup();return state,err}
		context.AfterFunc(runCtx,func(){session.Close()});wait=session.Wait
		dial=func(port int)(net.Conn,error){return client.Dial("tcp",net.JoinHostPort("127.0.0.1",strconv.Itoa(port)))}
		listener,err=net.Listen("tcp",net.JoinHostPort("127.0.0.1",strconv.Itoa(localPort)));if err!=nil{cleanup();session.Close();release();return state,err};context.AfterFunc(runCtx,func(){listener.Close()})
	}
	go func(){err:=wait();writer.Close();release();finished<-err;s.mu.Lock();if f:=s.kubeForwards[state.ID];f!=nil{f.state.Status="stopped";if err!=nil{f.state.Error=err.Error()}};s.mu.Unlock();cancel()}()
	go func(){scanner:=bufio.NewScanner(reader);scanner.Buffer(make([]byte,4096),65536);for scanner.Scan(){if match:=forwardAddress.FindStringSubmatch(scanner.Text());match!=nil{port,_:=strconv.Atoi(match[1]);select{case ready<-port:default:}}}}()
	var remotePort int
	select{case remotePort=<-ready:case err=<-finished:cleanup();if listener!=nil{listener.Close()};if err==nil{err=errors.New("port forward stopped before becoming ready")};return state,err;case <-ctx.Done():cleanup();return state,ctx.Err();case <-time.After(15*time.Second):cleanup();return state,errors.New("port forward did not become ready within 15 seconds")}
	state.Status="active";s.mu.Lock();if s.kubeForwards==nil{s.kubeForwards=map[string]*liveKubeForward{}};s.kubeForwards[state.ID]=&liveKubeForward{state:state,cancel:cleanup};s.mu.Unlock()
	if listener!=nil {go func(){slots:=make(chan struct{},32);for{connection,err:=listener.Accept();if err!=nil{return};select{case slots<-struct{}{}:case <-runCtx.Done():connection.Close();return};go func(){defer func(){<-slots}();defer connection.Close();stop:=context.AfterFunc(runCtx,func(){connection.Close()});defer stop();remote,err:=dial(remotePort);if err!=nil{return};defer remote.Close();stopRemote:=context.AfterFunc(runCtx,func(){remote.Close()});defer stopRemote();var wg sync.WaitGroup;wg.Add(2);go func(){defer wg.Done();io.Copy(remote,connection);remote.Close()}();go func(){defer wg.Done();io.Copy(connection,remote);connection.Close()}();wg.Wait()}()}}()}
	return state,nil
}
