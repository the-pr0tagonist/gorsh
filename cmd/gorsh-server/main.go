package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disneystreaming/gomux"
	"github.com/jessevdk/go-flags"
	"github.com/mattn/go-tty"
	log "github.com/sirupsen/logrus"
)

var (
    Iface string
    Port string
	Host string
)

var opts struct {
	Host      string `short:"H" long:"host" description:"IP address to bind to" required:"false"`
	Iface     string `short:"i" long:"interface" description:"Interface name to resolve IP from" required:"false"`
	Port      string `short:"p" long:"port" description:"Port to bind to" default:"13000" required:"true"`
	Keys      string `short:"k" long:"keys" description:"Path to folder with server.{pem,key}" default:"./certs" required:"true"`
	Socket    string `short:"s" long:"socket" description:"Domain socket to read from" required:"false"`
}

var sessions = make(map[string]*gomux.Session)

func init() {
	_, err := flags.Parse(&opts)
	// the flags package returns an error when calling --help for
	// some reason so we look for that and exit gracefully
	if err != nil {
		if err.(*flags.Error).Type == flags.ErrHelp {
			os.Exit(0)
		}
		log.Fatal(err)
	}

	// Since this binary only builds tmux commands and echoes them,
	// it needs to be piped to bash in order to work.
	// Because of this, all logging is sent to stderr
	log.SetOutput(os.Stderr)
	log.SetLevel(log.DebugLevel)

	// Ensure socket folder exists
	if _, err := os.Stat(".state"); os.IsNotExist(err) {
		os.Mkdir(".state", 0700)
	}

	// account for existing session to avoid 'duplicate session' error
	// initSessions()
}

func main() {
	if Iface != "" {
        opts.Iface = Iface
    }
	if Host != "" {
        opts.Host = Host
    }
    if Port != "" {
        opts.Port = Port
    }
	var listener net.Listener
	var err error

	if opts.Socket == "" {
		// Shell-catching mode. TLS -> TMUX -> Shell
		// Once the shell is caught over TLS, it's unwrapped and sent
		// to a local socket, where it will later be read by a new instance
		// of the server configured to read that socket from within a tmux pane
		listener, err = newTLSListener()
		if err != nil {
			log.Fatal(err)
		}

		bindAddress, err := getBindAddress()
		if err != nil {
			log.Fatal(err)
		}
		log.WithFields(log.Fields{"port": opts.Port, "host": bindAddress}).Info("Listener started")

		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Error("Listener Accept")
				continue
			}

			sockF, err := prepareTmux(conn)
			if err != nil {
				log.Error(err)
				continue
			}
			time.Sleep(10 * time.Second) // Give socket time to establish
			go proxyConnToSocket(conn, sockF)
			log.Info("Connection to socket successful")
		}

	} else {

		// Post-tmux routing.
		// Creates a socket file and listens for input.
		// If in this branch, binary was started from within tmux.
		// Once the tcp and sockets are mutually proxied with
		// `proxyConnToSocket`, the shell will start
		listener, err = net.Listen("unix", opts.Socket)
		if err != nil {
			log.Fatal(err)
		}
		defer listener.Close()
		log.WithField("socket", opts.Socket).Info("Listener started")

		conn, err := listener.Accept()
		if err != nil {
			log.Error(err)
		}
		startShell(conn)
	}
}

func getBindAddress() (string, error) {
	if opts.Iface != "" {
		// Si --interface est spécifié, récupérer l'IP de l'interface
		ip, err := getInterfaceIP(opts.Iface)
		if err != nil {
			return "", err
		}
		return ip, nil
	} else if opts.Host != "" {
		// Si --host est spécifié, utiliser cette IP
		return opts.Host, nil
	} 
	return "", fmt.Errorf("Either --host or --interface must be specified")
}

func getInterfaceIP(interfaceName string) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if iface.Name == interfaceName {
			addrs, err := iface.Addrs()
			if err != nil {
				return "", err
			}

			for _, addr := range addrs {
				switch v := addr.(type) {
				case *net.IPNet:
					if v.IP.To4() != nil { // Vérifier l'adresse IPv4
						return v.IP.String(), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("Interface %s not found", interfaceName)
}

func newTLSListener() (net.Listener, error) {
	pem := path.Join(opts.Keys, "server.pem")
	key := path.Join(opts.Keys, "server.key")
	cer, err := tls.LoadX509KeyPair(pem, key)
	if err != nil {
		log.Fatal(err)
	}

	// Obtenir l'adresse IP de l'interface ou de l'hôte
	bindAddress, err := getBindAddress()
	if err != nil {
		log.Fatal(err)
	}

	// Utiliser l'adresse IP obtenue dans connStr
	connStr := fmt.Sprintf("%s:%s", bindAddress, opts.Port)
	log.Infof("Binding to IP: %s on port %s", bindAddress, opts.Port)

	config := &tls.Config{Certificates: []tls.Certificate{cer}}
	return tls.Listen("tcp", connStr, config)
}


func startShell(conn net.Conn) {
	log.WithFields(log.Fields{"port": opts.Port, "host": opts.Iface}).Info("Incoming")
	defer conn.Close()

	ttwhy, err := tty.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer ttwhy.Close()

	// unraw, err := ttwhy.Raw()
	// if err != nil {
	// 	log.Error(err)
	// }
	// defer unraw()

	// continuously print shell stdout coming from the implant
	go func() { io.Copy(ttwhy.Output(), conn) }()

	// blocking call to read user input
	io.Copy(conn, ttwhy.Input())
}

func implantInfo(conn net.Conn) (hostname, username string, err error) {
	reader := bufio.NewReader(conn)
	hostname, err = reader.ReadString('\n')
	if err != nil {
		err = fmt.Errorf("Hostname read failed: %w", err)
		return
	}
	hostname = sanitizeforTmux(hostname)

	username, err = reader.ReadString('\n')
	if err != nil {
		err = fmt.Errorf("Username read failed: %w", err)
		return
	}

	username = sanitizeforTmux(username)
	return
}

func genTempFilename(username string) (string, error) {
	file, err := os.CreateTemp(".state", fmt.Sprintf("%s.*.sock", username))
	if err != nil {
		err = fmt.Errorf("Temp file failed: %w", err)
		return "", err
	}
	os.Remove(file.Name())

	path, err := filepath.Abs(file.Name())
	if err != nil {
		err = fmt.Errorf("Temp path read failed: %w", err)
		return "", err
	}
	return path, nil
}

func prepareTmux(conn net.Conn) (string, error) {
	hostname, username, err := implantInfo(conn)
	if err != nil {
		return "", fmt.Errorf("Failed getting implant info: %w", err)
	}

	// Vérification de l'existence de la session avec une gestion spécifique de l'erreur exit status 1
	exists, err := gomux.CheckSessionExists(hostname)
	if err != nil {
		log.Warn(err)

		// Si l'erreur est liée à "exit status 1" (tmux n'est pas en cours d'exécution), on ignore et continue
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			log.Warn("Tmux not running, assuming session doesn't exist: %v", err)
			exists = false // On considère que la session n'existe pas encore
		} else {
			// Si l'erreur est autre, on la retourne
			err = fmt.Errorf("CheckSessionExists: %w", err)
			return "", err
		}
	}

	// Si la session n'existe pas encore
	if !exists {
		log.WithField("host", hostname).Info("New host connected, creating session")
		sessions[hostname], err = gomux.NewSession(hostname)
		if err != nil {
			log.Warn("Error creating new session: ", err)
		}
	}

	// Session existe déjà, mais n'est pas encore suivie
	if exists && sessions[hostname] == nil {
		log.WithField("host", hostname).Debug("Creating new cached session")
		sessions[hostname] = &gomux.Session{Name: hostname}
	}

	session := sessions[hostname]
	id := fmt.Sprintf("%s.%d", username, session.NextWindowNumber+1)
	window, err := session.AddWindow(id)
	if err != nil {
		log.WithFields(
			log.Fields{"session": session.Name, "window": window},
		).Warn("AddWindow(Id) ", err)
	}

	path, err := genTempFilename(username)
	if err != nil {
		err = fmt.Errorf("genTempFilename: %w", err)
		return "", err
	}

	err = window.Panes[0].Exec(`echo -e '\a'`) // ring a bell
	if err != nil {
		log.WithFields(
			log.Fields{"session": session.Name, "window": id, "path": path},
		).Warn("Exec echo: ", err)
	}

	self := os.Args[0]
	cmd := fmt.Sprintf("%s -s %s", self, path)

	err = window.Panes[0].Exec(cmd)
	if err != nil {
		log.WithFields(
			log.Fields{"session": session.Name, "window": id, "cmd": cmd},
		).Warn("Exec cmd: ", err)
	}

	log.WithFields(log.Fields{"session": session.Name, "window": username}).
		Info("New shell in tmux. Connecting to socket... ")
	return path, nil
}


func proxyConnToSocket(conn net.Conn, sockF string) {
	socket, err := net.Dial("unix", sockF)
	if err != nil {
		log.WithField("err", err).Error("Failed to dial sockF")
		return
	}
	defer socket.Close()
	defer os.Remove(sockF)
	wg := sync.WaitGroup{}

	// forward socket to tcp
	wg.Add(1)
	go (func(socket net.Conn, conn net.Conn) {
		defer conn.Close()
		defer wg.Done()
		io.Copy(conn, socket)
	})(socket, conn)

	// forward tcp to socket
	wg.Add(1)
	go (func(socket net.Conn, conn net.Conn) {
		defer socket.Close()
		defer wg.Done()
		io.Copy(socket, conn)
	})(socket, conn)
	// keep from returning until sockets close so we
	// can cleanup the socket file using `defer`
	wg.Wait()
}

func sanitizeforTmux(in string) (data string) {
	// tmux session names can't contain ".", "\", " "
	// windows gets usernames by [domain|computer]\\user.
	data = strings.TrimSuffix(in, "\n")
	data = strings.ReplaceAll(data, ".", "_")
	data = strings.ReplaceAll(data, `\`, "_")
	data = strings.ReplaceAll(data, ` `, "-")
	return
}
