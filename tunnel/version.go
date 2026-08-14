package tunnel

// Version 是 tunnel 的版本号。
// 默认为 "dev";发布构建时由 CI 通过 -ldflags 注入 tag:
//
//	go build -ldflags "-X github.com/pangxincheng/tunnel/tunnel.Version=v1.2.3"
//
// 注意必须保持为 var(可写),const 无法被 -X 覆盖。
var Version = "dev"
