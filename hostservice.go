package main

import "github.com/blacknode/blacknode/internal/store"

type HostService struct {
	hosts *store.Hosts
}

func NewHostService(h *store.Hosts) *HostService {
	return &HostService{hosts: h}
}

func (s *HostService) List() ([]store.Host, error)         { return s.hosts.List() }
func (s *HostService) Get(id string) (store.Host, error)   { return s.hosts.Get(id) }
func (s *HostService) Create(h store.Host) (store.Host, error) { return s.hosts.Create(h) }
func (s *HostService) Update(h store.Host) error           { return s.hosts.Update(h) }
func (s *HostService) Delete(id string) error              { return s.hosts.Delete(id) }
