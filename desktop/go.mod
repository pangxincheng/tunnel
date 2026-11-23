module github.com/pangxincheng/tunnel-app/desktop

go 1.26.0

require (
	github.com/pangxincheng/tunnel v1.2.0
	github.com/wailsapp/wails/v3 v3.1.0
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/docopt/docopt-go v0.0.0-20180111231733-ee0de3bc6815 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/pkg/sftp v1.13.12 // indirect
	golang.org/x/crypto v0.57.0
)

require (
	github.com/coder/websocket v1.8.14 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
)

replace github.com/pangxincheng/tunnel => ../../tunnel

tool github.com/pangxincheng/tunnel/cmd/tunnel
