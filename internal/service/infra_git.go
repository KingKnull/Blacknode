package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type GitRunbookRepository struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Directory string `json:"directory"`
	File      string `json:"file"`
	Remote    string `json:"remote"`
	Branch    string `json:"branch"`
}
type GitRunbookState struct {
	Commit string    `json:"commit"`
	Status string    `json:"status"`
	Diff   string    `json:"diff"`
	YAML   string    `json:"yaml"`
	Books  []Runbook `json:"books"`
}
type runbookYAML struct {
	Version  int       `json:"version"`
	Runbooks []Runbook `json:"runbooks"`
}

var gitCommitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
var gitRemoteName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,100}$`)

func gitCommand(ctx context.Context, directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr cappedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git: %s: %w", redactEvidence(stderr.String()), err)
	}
	if stdout.overflow || stderr.overflow {
		return "", errors.New("git output exceeded limit")
	}
	return stdout.String(), nil
}
func validateGitRepository(ctx context.Context, p GitRunbookRepository) error {
	if p.Name == "" || len(p.Name) > 120 || !filepath.IsAbs(p.Directory) || !safeSyncPath(p.File) || strings.HasPrefix(p.File, ".git/") || p.File == ".git" || !gitRemoteName.MatchString(p.Remote) || strings.HasPrefix(p.Branch, "-") || p.Branch == "" || len(p.Branch) > 200 {
		return errors.New("name, existing repository, relative YAML file, remote and branch required")
	}
	if _, err := gitCommand(ctx, p.Directory, "check-ref-format", "--branch", p.Branch); err != nil {
		return err
	}
	root, err := gitCommand(ctx, p.Directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(p.Directory)
	if err != nil {
		return err
	}
	if filepath.Clean(strings.TrimSpace(root)) != canonical {
		return errors.New("select the repository root directory")
	}
	return nil
}
func (s *InfrastructureService) SaveGitRepository(ctx context.Context, p GitRunbookRepository, revision int64) (store.OperationDocument, error) {
	if err := validateGitRepository(ctx, p); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return s.platform.store.Put("git-runbooks", p.ID, revision, p)
}
func encodeRunbookYAML(books []Runbook) (string, error) {
	raw, err := json.Marshal(runbookYAML{Version: 1, Runbooks: books})
	if err != nil {
		return "", err
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(value)
	return string(data), err
}
func decodeRunbookYAML(data string) ([]Runbook, error) {
	if len(data) > 1<<20 {
		return nil, errors.New("runbook YAML exceeds 1 MiB")
	}
	var value any
	decoder := yaml.NewDecoder(strings.NewReader(data))
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("use one YAML document")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document runbookYAML
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&document); err != nil {
		return nil, err
	}
	if document.Version != 1 || len(document.Runbooks) > 500 {
		return nil, errors.New("unsupported runbook document version or size")
	}
	ids := map[string]bool{}
	for _, book := range document.Runbooks {
		if book.ID == "" || ids[book.ID] {
			return nil, errors.New("runbooks need unique IDs")
		}
		ids[book.ID] = true
		if err = validateRunbook(book); err != nil {
			return nil, err
		}
	}
	if document.Runbooks == nil {
		document.Runbooks = []Runbook{}
	}
	return document.Runbooks, nil
}
func (s *InfrastructureService) gitRepository(ctx context.Context, id string) (GitRunbookRepository, error) {
	var p GitRunbookRepository
	err := s.platform.store.Read("git-runbooks", id, &p)
	if err == nil {
		err = validateGitRepository(ctx, p)
	}
	return p, err
}
func (s *InfrastructureService) GitRunbooks(ctx context.Context, id, ref string) (GitRunbookState, error) {
	p, err := s.gitRepository(ctx, id)
	if err != nil {
		return GitRunbookState{}, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	if strings.HasPrefix(ref, "-") || len(ref) > 240 || strings.ContainsAny(ref, "\x00\r\n:") {
		return GitRunbookState{}, errors.New("invalid Git reference")
	}
	hash, err := gitCommand(ctx, p.Directory, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return GitRunbookState{}, err
	}
	state := GitRunbookState{Commit: strings.TrimSpace(hash)}
	if !gitCommitID.MatchString(state.Commit) {
		return state, errors.New("invalid commit hash")
	}
	state.Status, err = gitCommand(ctx, p.Directory, "status", "--short", "--", p.File)
	if err != nil {
		return state, err
	}
	state.Diff, err = gitCommand(ctx, p.Directory, "diff", "HEAD", "--", p.File)
	if err != nil {
		return state, err
	}
	state.YAML, err = gitCommand(ctx, p.Directory, "show", state.Commit+":"+p.File)
	if err != nil {
		return state, err
	}
	state.Books, err = decodeRunbookYAML(state.YAML)
	return state, err
}
func (s *InfrastructureService) FetchGitRunbooks(ctx context.Context, id string) (string, error) {
	p, err := s.gitRepository(ctx, id)
	if err != nil {
		return "", err
	}
	return gitCommand(ctx, p.Directory, "fetch", "--no-tags", p.Remote, p.Branch)
}
func (s *InfrastructureService) PublishGitRunbooks(ctx context.Context, id string) (string, error) {
	p, err := s.gitRepository(ctx, id)
	if err != nil {
		return "", err
	}
	branch, err := gitCommand(ctx, p.Directory, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(branch) != p.Branch {
		return "", errors.New("checkout the configured branch before publishing")
	}
	return gitCommand(ctx, p.Directory, "push", p.Remote, "HEAD:refs/heads/"+p.Branch)
}

func (s *InfrastructureService) reviewOperation(title, scope, description, preview string, apply func(context.Context) error) (ChangePlan, error) {
	plan, err := s.platform.prepare("", scope, title, description, "", preview, 60, nil)
	if err != nil {
		return plan, err
	}
	s.platform.mu.Lock()
	change := s.platform.plans[plan.ID]
	change.apply = func(ctx context.Context) ExecResult {
		start := time.Now()
		result := ExecResult{HostName: "Local", ExitCode: 0}
		if err := apply(ctx); err != nil {
			result.ExitCode = -1
			result.Error = err.Error()
		} else {
			result.Stdout = "Completed"
		}
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	s.platform.plans[plan.ID] = change
	s.platform.mu.Unlock()
	return plan, nil
}
func (s *InfrastructureService) PreviewGitExport(ctx context.Context, id, message string) (ChangePlan, error) {
	p, err := s.gitRepository(ctx, id)
	if err != nil {
		return ChangePlan{}, err
	}
	if strings.TrimSpace(message) == "" || len(message) > 1000 {
		return ChangePlan{}, errors.New("commit message required")
	}
	status, err := gitCommand(ctx, p.Directory, "status", "--porcelain", "--", p.File)
	if err != nil {
		return ChangePlan{}, err
	}
	if strings.TrimSpace(status) != "" {
		return ChangePlan{}, errors.New("commit or move changes in the runbook file before exporting")
	}
	books, err := s.books.List(ctx)
	if err != nil {
		return ChangePlan{}, err
	}
	for i := range books {
		books[i].Revision = 0
		books[i].SourceCommit = ""
		books[i].SourceRepository = ""
	}
	content, err := encodeRunbookYAML(books)
	if err != nil {
		return ChangePlan{}, err
	}
	root, err := os.OpenRoot(p.Directory)
	if err != nil {
		return ChangePlan{}, err
	}
	defer root.Close()
	original, readErr := root.ReadFile(filepath.FromSlash(p.File))
	if readErr != nil && !os.IsNotExist(readErr) {
		return ChangePlan{}, readErr
	}
	head, err := gitCommand(ctx, p.Directory, "rev-parse", "HEAD")
	if err != nil {
		return ChangePlan{}, err
	}
	return s.reviewOperation("Export runbooks and commit", id, "Write "+p.File+" and commit only this file", content, func(ctx context.Context) error {
		status, e := gitCommand(ctx, p.Directory, "status", "--porcelain", "--", p.File)
		if e != nil {
			return e
		}
		if strings.TrimSpace(status) != "" {
			return errors.New("runbook file or index changed; review again")
		}
		current, e := gitCommand(ctx, p.Directory, "rev-parse", "HEAD")
		if e != nil {
			return e
		}
		if current != head {
			return errors.New("repository HEAD changed; review again")
		}
		r, e := os.OpenRoot(p.Directory)
		if e != nil {
			return e
		}
		defer r.Close()
		now, e := r.ReadFile(filepath.FromSlash(p.File))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if !bytes.Equal(now, original) || os.IsNotExist(e) != os.IsNotExist(readErr) {
			return errors.New("runbook file changed; review again")
		}
		if e = r.MkdirAll(filepath.Dir(filepath.FromSlash(p.File)), 0700); e != nil {
			return e
		}
		temp := p.File + ".blacknode-" + uuid.NewString()
		f, e := r.OpenFile(filepath.FromSlash(temp), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer r.Remove(filepath.FromSlash(temp))
		_, e = f.WriteString(content)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if e = r.Rename(filepath.FromSlash(temp), filepath.FromSlash(p.File)); e != nil {
			return e
		}
		if _, e = gitCommand(ctx, p.Directory, "add", "--", p.File); e != nil {
			return e
		}
		_, e = gitCommand(ctx, p.Directory, "commit", "--only", "-m", message, "--", p.File)
		return e
	})
}
func (s *InfrastructureService) pinnedGitBook(ctx context.Context, id, commit, bookID string) (Runbook, error) {
	if !gitCommitID.MatchString(commit) {
		return Runbook{}, errors.New("choose an exact commit before importing or running")
	}
	state, err := s.GitRunbooks(ctx, id, commit)
	if err != nil {
		return Runbook{}, err
	}
	for _, book := range state.Books {
		if book.ID == bookID {
			book.SourceCommit = state.Commit
			book.SourceRepository = id
			return book, nil
		}
	}
	return Runbook{}, errors.New("runbook not found in commit")
}
func (s *InfrastructureService) PreviewGitImport(ctx context.Context, id, commit, bookID string) (ChangePlan, error) {
	book, err := s.pinnedGitBook(ctx, id, commit, bookID)
	if err != nil {
		return ChangePlan{}, err
	}
	book.Revision = 0
	if d, e := s.platform.store.Get("runbook", book.ID); e == nil {
		book.Revision = d.Revision
	}
	preview, _ := encodeRunbookYAML([]Runbook{book})
	return s.reviewOperation("Import "+book.Name, id, "Import runbook from commit "+commit, preview, func(ctx context.Context) error { _, err := s.books.Save(ctx, book); return err })
}
func (s *InfrastructureService) PreviewGitExecution(ctx context.Context, id, commit, bookID string, hostIDs []string, values map[string]string) (ChangePlan, error) {
	book, err := s.pinnedGitBook(ctx, id, commit, bookID)
	if err != nil {
		return ChangePlan{}, err
	}
	steps, err := s.books.Preview(ctx, book, values)
	if err != nil {
		return ChangePlan{}, err
	}
	if len(hostIDs) == 0 {
		return ChangePlan{}, errors.New("select hosts")
	}
	names := []string{}
	production := []string{}
	for _, id := range hostIDs {
		h, e := s.platform.hosts.GetResolved(id)
		if e != nil {
			return ChangePlan{}, e
		}
		names = append(names, h.Name+" ["+h.Environment+"]")
		if h.Environment == "production" {
			production = append(production, h.Name)
		}
	}
	preview, _ := json.MarshalIndent(steps, "", "  ")
	plan, err := s.reviewOperation("Run "+book.Name, id, "Run commit "+commit+" on "+strings.Join(names, ", "), string(preview), func(ctx context.Context) error { _, err := s.jobs.Start(ctx, book, hostIDs, values); return err })
	if err == nil {
		plan.ProductionHosts = production
		s.platform.mu.Lock()
		change := s.platform.plans[plan.ID]
		change.plan = plan
		s.platform.plans[plan.ID] = change
		s.platform.mu.Unlock()
	}
	return plan, err
}
