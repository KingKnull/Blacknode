package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type AIProviderConfig struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	CommandModel string `json:"commandModel"`
	AnalysisModel string `json:"analysisModel"`
}

func (s *SettingsService) AIProvider(ctx context.Context) (AIProviderConfig,error){
	cfg:=AIProviderConfig{Provider:"anthropic",Endpoint:"http://127.0.0.1:11434",CommandModel:string(modelTranslate),AnalysisModel:string(modelExplain)}
	raw,err:=s.settings.GetPlain("ai.provider.v1");if err!=nil{return cfg,err};if raw!=""{err=json.Unmarshal([]byte(raw),&cfg)};return cfg,err
}

func (s *SettingsService) SetAIProvider(ctx context.Context,cfg AIProviderConfig)error{
	if cfg.Provider!="anthropic" && cfg.Provider!="ollama"{return errors.New("choose Anthropic or Ollama")}
	if cfg.Provider=="ollama"{if _,err:=validateEndpoint(cfg.Endpoint);err!=nil{return err}}
	if strings.TrimSpace(cfg.CommandModel)=="" || strings.TrimSpace(cfg.AnalysisModel)=="" || len(cfg.CommandModel)>160 || len(cfg.AnalysisModel)>160{return errors.New("command and analysis model names required")}
	raw,err:=json.Marshal(cfg);if err!=nil{return err};return s.settings.SetPlain("ai.provider.v1",string(raw))
}

func (s *AIService) generate(ctx context.Context,system,prompt string,analysis bool)(string,error){
	cfg,err:=s.settings.AIProvider(ctx);if err!=nil{return "",err}
	model:=cfg.CommandModel;tokens:=int64(1024);if analysis{model=cfg.AnalysisModel;tokens=8192}
	if cfg.Provider=="ollama" {response,err:=s.ollamaRequest(ctx,cfg,model,system,prompt,false);if err!=nil{return "",err};defer response.Body.Close();data,err:=readBounded(response.Body);if err!=nil{return "",err};var out struct{Message struct{Content string `json:"content"`} `json:"message"`;Error string `json:"error"`};if err=json.Unmarshal(data,&out);err!=nil{return "",err};if out.Error!=""{return "",errors.New(out.Error)};return strings.TrimSpace(out.Message.Content),nil}
	client,err:=s.client();if err!=nil{return "",err};response,err:=client.Messages.New(ctx,anthropic.MessageNewParams{Model:anthropic.Model(model),MaxTokens:tokens,System:[]anthropic.TextBlockParam{{Text:system}},Messages:[]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))}})
	if err!=nil{return "",err};var out strings.Builder;for _,block:=range response.Content{if text,ok:=block.AsAny().(anthropic.TextBlock);ok{out.WriteString(text.Text)}};return strings.TrimSpace(out.String()),nil
}

func (s *AIService) ollamaRequest(ctx context.Context,cfg AIProviderConfig,model,system,prompt string,stream bool)(*http.Response,error){
	u,err:=validateEndpoint(cfg.Endpoint);if err!=nil{return nil,err};u.Path=strings.TrimRight(u.Path,"/")+"/api/chat"
	data,err:=json.Marshal(map[string]any{"model":model,"stream":stream,"messages":[]map[string]string{{"role":"system","content":system},{"role":"user","content":prompt}}});if err!=nil{return nil,err}
	request,err:=http.NewRequestWithContext(ctx,"POST",u.String(),bytes.NewReader(data));if err!=nil{return nil,err};request.Header.Set("Content-Type","application/json")
	client:=&http.Client{Timeout:3*time.Minute,CheckRedirect:func(req *http.Request,via []*http.Request)error{return http.ErrUseLastResponse}}
	response,err:=client.Do(request);if err!=nil{return nil,err};if response.StatusCode!=http.StatusOK{defer response.Body.Close();data,_:=readBounded(response.Body);return nil,fmt.Errorf("Ollama returned HTTP %d: %s",response.StatusCode,string(data[:min(len(data),500)]))};return response,nil
}

func (s *AIService) explainOllama(ctx context.Context,cfg AIProviderConfig,streamID,content string)error{
	s.cancel(streamID);ctx,cancel:=context.WithCancel(context.Background());s.mu.Lock();s.cancels[streamID]=cancel;s.mu.Unlock()
	go func(){defer s.cancel(streamID);emit:=func(delta string,done bool,err string){if a:=application.Get();a!=nil{a.Event.Emit("ai:chunk",AIChunk{StreamID:streamID,Delta:delta,Done:done,Error:err})}}
		response,err:=s.ollamaRequest(ctx,cfg,cfg.AnalysisModel,systemExplain,redactEvidence(content),true);if err!=nil{emit("",true,err.Error());return};defer response.Body.Close()
		scanner:=bufio.NewScanner(response.Body);scanner.Buffer(make([]byte,65536),1<<20)
		for scanner.Scan(){var part struct{Message struct{Content string `json:"content"`} `json:"message"`;Done bool `json:"done"`;Error string `json:"error"`};if err=json.Unmarshal(scanner.Bytes(),&part);err!=nil{emit("",true,err.Error());return};emit(part.Message.Content,part.Done,part.Error);if part.Done || part.Error!=""{return}}
		if err=scanner.Err();err!=nil{emit("",true,err.Error())}else{emit("",true,"")}
	}();return nil
}
