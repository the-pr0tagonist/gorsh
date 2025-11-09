package pivot

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"os/user"
	"fmt"
	"net"
	"syscall"
	"time"
	"github.com/nicocha30/ligolo-ng/pkg/agent/neterror"
	"github.com/nicocha30/ligolo-ng/pkg/agent/smartping"
	"github.com/nicocha30/ligolo-ng/pkg/protocol" // Changement de module
	"github.com/nicocha30/ligolo-ng/pkg/relay"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"
)

var listenerConntrack map[int32]net.Conn
var listenerMap map[int32]net.Listener
var connTrackID       int32
var listenerID        int32
var sessionID         string
var (
	retry     = true
	tlsConfig tls.Config


)

func init() {
	id := uuid.New()
	sessionID = id.String()
}


func Connect(host string) error {
	tlsConfig.ServerName = host
	tlsConfig.InsecureSkipVerify = true

	for {
		conn, err := net.Dial("tcp", host)
		if err == nil {
			err = connect(conn, &tlsConfig)
		}
		logrus.Errorf("Connection error: %v", err)
		if retry {
			time.Sleep(5 * time.Second)
		} else {
			return err
		}
	}
}

func connect(conn net.Conn, config *tls.Config) error {
	tlsConn := tls.Client(conn, config)

	yamuxConn, err := yamux.Server(tlsConn, yamux.DefaultConfig())
	if err != nil {
		return err
	}

	for {
		conn, err := yamuxConn.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn)
	}
}

// Listener is the base class implementing listener sockets for Ligolo
type Listener struct {
	net.Listener
}

// NewListener register a new listener
func NewListener(network string, addr string) (Listener, error) {
	lis, err := net.Listen(network, addr)
	if err != nil {
		return Listener{}, err
	}
	return Listener{lis}, nil
}

// ListenAndServe fill new listener connections to a channel
func (s *Listener) ListenAndServe(connTrackChan chan int32) error {
	for {
		conn, err := s.Accept()
		if err != nil {
			return err
		}
		connTrackID++
		connTrackChan <- connTrackID
		listenerConntrack[connTrackID] = conn
	}
}

// Close request the main listener to exit
func (s *Listener) Close() error {
	return s.Listener.Close()
}

func HandleConn(conn net.Conn) {
	decoder := protocol.NewDecoder(conn)
	if err := decoder.Decode(); err != nil {
		logrus.Error(err)
		return
	}

	e := decoder
	switch decoder.Payload.(type) {

	case *protocol.ConnectRequestPacket:
		connRequest := e.Payload.(*protocol.ConnectRequestPacket)
		encoder := protocol.NewEncoder(conn)

		logrus.Debugf("Got connect request to %s:%d", connRequest.Address, connRequest.Port)
		var network string
		if connRequest.Transport == protocol.TransportTCP {
			network = "tcp"
		} else {
			network = "udp"
		}
		if connRequest.Net == protocol.Networkv4 {
			network += "4"
		} else {
			network += "6"
		}

		var d net.Dialer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		targetConn, err := d.DialContext(ctx, network, fmt.Sprintf("%s:%d", connRequest.Address, connRequest.Port))
		defer cancel()

		var connectPacket protocol.ConnectResponsePacket
		if err != nil {

			var serr syscall.Errno
			if errors.As(err, &serr) {
				// Magic trick ! If the error syscall indicate that the system responded, send back a RST packet!
				if neterror.HostResponded(serr) {
					connectPacket.Reset = true
				}
			}

			connectPacket.Established = false
		} else {
			connectPacket.Established = true
		}
		if err := encoder.Encode(connectPacket); err != nil {
			logrus.Error(err)
			return
		}
		if connectPacket.Established {
			relay.StartRelay(targetConn, conn)
		}
	case *protocol.HostPingRequestPacket:
		pingRequest := e.Payload.(*protocol.HostPingRequestPacket)
		encoder := protocol.NewEncoder(conn)

		pingResponse := protocol.HostPingResponsePacket{Alive: smartping.TryResolve(pingRequest.Address)}

		if err := encoder.Encode(pingResponse); err != nil {
			logrus.Error(err)
			return
		}
	case *protocol.InfoRequestPacket:
		var username string
		encoder := protocol.NewEncoder(conn)
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "UNKNOWN"
		}

		userinfo, err := user.Current()
		if err != nil {
			username = "Unknown"
		} else {
			username = userinfo.Username
		}

		netifaces, err := net.Interfaces()
		if err != nil {
			logrus.Error("could not get network interfaces")
			return
		}
		infoResponse := protocol.InfoReplyPacket{
			Name:       fmt.Sprintf("%s@%s", username, hostname),
			Interfaces: protocol.NewNetInterfaces(netifaces),
			SessionID:  sessionID,
		}

		if err := encoder.Encode(infoResponse); err != nil {
			logrus.Error(err)
			return
		}
	case *protocol.ListenerCloseRequestPacket:
		// Request to close a listener
		closeRequest := e.Payload.(*protocol.ListenerCloseRequestPacket)
		encoder := protocol.NewEncoder(conn)

		var err error
		if lis, ok := listenerMap[closeRequest.ListenerID]; ok {
			if l, ok := lis.(net.Listener); ok {
				l.Close()
			}
		} else {
			err = errors.New("invalid listener id")
		}

		listenerResponse := protocol.ListenerCloseResponsePacket{
			Err: err != nil,
		}
		if err != nil {
			listenerResponse.ErrString = err.Error()
		}

		if err := encoder.Encode(listenerResponse); err != nil {
			logrus.Error(err)
		}

	case *protocol.ListenerRequestPacket:
		listenRequest := e.Payload.(*protocol.ListenerRequestPacket)
		encoder := protocol.NewEncoder(conn)
		connTrackChan := make(chan int32)
		stopChan := make(chan error)

		if listenRequest.Network == "tcp" {
			listener, err := NewListener(listenRequest.Network, listenRequest.Address)
			if err != nil {
				listenerResponse := protocol.ListenerResponsePacket{
					ListenerID: 0,
					Err:        true,
					ErrString:  err.Error(),
				}
				if err := encoder.Encode(listenerResponse); err != nil {
					logrus.Error(err)
				}
				return
			}
			listenerMap[listenerID] = listener.Listener
			listenerResponse := protocol.ListenerResponsePacket{
				ListenerID: listenerID,
				Err:        false,
				ErrString:  "",
			}
			if err := encoder.Encode(listenerResponse); err != nil {
				logrus.Error(err)
			}
			go func() {
				if err := listener.ListenAndServe(connTrackChan); err != nil {
					stopChan <- err
				}
			}()
			defer listener.Close()

		}

		listenerID++
		if listenRequest.Network == "tcp" {
			for {
				var bindResponse protocol.ListenerBindReponse
				select {
				case err := <-stopChan:
					logrus.Error(err)
					bindResponse = protocol.ListenerBindReponse{
						SockID:    0,
						Err:       true,
						ErrString: err.Error(),
					}
				case connTrackID := <-connTrackChan:
					bindResponse = protocol.ListenerBindReponse{
						SockID: connTrackID,
						Err:    false,
					}
				}

				if err := encoder.Encode(bindResponse); err != nil {
					logrus.Error(err)
				}

				if bindResponse.Err {
					break
				}

			}
		}
	case *protocol.ListenerSockRequestPacket:
		sockRequest := e.Payload.(*protocol.ListenerSockRequestPacket)
		encoder := protocol.NewEncoder(conn)

		var sockResponse protocol.ListenerSockResponsePacket
		if _, ok := listenerConntrack[sockRequest.SockID]; !ok {
			// Handle error
			sockResponse.ErrString = "invalid or unexistant SockID"
			sockResponse.Err = true
		}

		if err := encoder.Encode(sockResponse); err != nil {
			logrus.Error(err)
			return
		}

		if sockResponse.Err {
			return
		}

		netConn := listenerConntrack[sockRequest.SockID]
		relay.StartRelay(netConn, conn)

	case *protocol.ListenerCloseResponsePacket:
		os.Exit(0)

	}
}
