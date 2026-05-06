package mockssh

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Server is an in-memory SSH server for integration testing.
type Server struct {
	Listener net.Listener
	Config   *ssh.ServerConfig
	PublicKey ssh.PublicKey

	// Handlers map command prefixes to an output and an exit status
	Handlers map[string]func(cmd string) (string, uint32)
}

func NewServer() (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &Server{
		Listener:  listener,
		Config:    config,
		PublicKey: signer.PublicKey(),
		Handlers:  make(map[string]func(cmd string) (string, uint32)),
	}

	go s.serve()
	return s, nil
}

func (s *Server) Port() int {
	return s.Listener.Addr().(*net.TCPAddr).Port
}

func (s *Server) Addr() string {
	return s.Listener.Addr().String()
}

func (s *Server) Close() {
	s.Listener.Close()
}

func (s *Server) serve() {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	_, chans, reqs, err := ssh.NewServerConn(conn, s.Config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go s.handleSessionRequests(channel, requests)
	}
}

func (s *Server) handleSessionRequests(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		if req.Type == "exec" {
			// Payload is a string length followed by the command string
			if len(req.Payload) >= 4 {
				cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
				if len(req.Payload) >= int(4+cmdLen) {
					cmd := string(req.Payload[4 : 4+cmdLen])
					s.handleExec(channel, cmd)
					req.Reply(true, nil)
					return // exec terminates the session
				}
			}
		}
		if req.WantReply {
			req.Reply(false, nil)
		}
	}
}

func (s *Server) handleExec(channel ssh.Channel, cmd string) {
	var output string
	var exitStatus uint32 = 0

	for prefix, handler := range s.Handlers {
		if strings.HasPrefix(cmd, prefix) {
			output, exitStatus = handler(cmd)
			break
		}
	}

	if output != "" {
		channel.Write([]byte(output))
	}

	// Send exit status
	exitMsg := struct {
		Status uint32
	}{Status: exitStatus}
	channel.SendRequest("exit-status", false, ssh.Marshal(exitMsg))
}
