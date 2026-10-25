// Command codex-bridge 是 sub4api 的 OpenAI OAuth 出站传输插件。
// 入口只负责启动 go-plugin 进程协议；全部逻辑在 internal/ 包内，可独立测试。
package main

import (
	"log"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/transport"
	pluginv1 "github.com/MACOS-DO/sub4api/pkg/pluginapi/v1"
)

// version 由打包脚本注入（-ldflags "-X main.version=..."）。
var version = "0.1.0"

func main() {
	impl, err := transport.NewServer("sub4api.codex-bridge", version, pluginconfig.Default(), metrics.New())
	if err != nil {
		log.Fatalf("codex-bridge: 初始化失败: %v", err)
	}
	pluginv1.Serve(impl)
}
