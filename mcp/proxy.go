package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// HTTPProxy 统一的 MCP HTTP 服务代理
// 为每个注册的 MCP server HTTP 服务提供反向代理 + Go 客户端转发能力
type HTTPProxy struct {
	mu     sync.RWMutex
	routes map[string]string // serverID → targetURL
	addr   string            // 监听地址如 ":1860"
	server *http.Server
	mux    *http.ServeMux
	client *http.Client
}

// NewHTTPProxy 创建 HTTP 代理
func NewHTTPProxy() *HTTPProxy {
	return &HTTPProxy{
		routes: make(map[string]string),
		mux:    http.NewServeMux(),
		client: &http.Client{},
	}
}

// Register 注册 MCP server 的 HTTP 服务
// id: MCP server ID, targetURL: 如 "http://127.0.0.1:9749"
func (p *HTTPProxy) Register(id, targetURL string) {
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Printf("[HTTPProxy] 注册失败，无效 URL %s: %v", targetURL, err)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.routes[id] = targetURL
	prefix := "/mcp/" + id + "/"

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(r *http.Response) error {
		r.Header.Del("Content-Security-Policy")
		r.Header.Del("X-Frame-Options")
		// 在 HTML 响应中注入桥接脚本，使 API 请求通过父窗口的 Wails IPC 转发
		if isHTMLContent(r.Header.Get("Content-Type")) {
			oldBody := r.Body
			r.Body = io.NopCloser(&bridgeInjectionReader{
				reader:   oldBody,
				serverID: id,
			})
		}
		return nil
	}
	p.mux.Handle(prefix, http.StripPrefix(prefix, proxy))
	log.Printf("[HTTPProxy] 注册路由 %s → %s", prefix, targetURL)
}

// BridgeScript 返回桥接 JS 脚本，将 API 请求通过 postMessage 发给父窗口
func BridgeScript(serverID string) string {
	return fmt.Sprintf(`<script>
(function(){
var sid=%q;
var origFetch=window.fetch;
window.fetch=function(u,o){
var url=typeof u==='string'?u:(u.url||'');
if(url.startsWith('/api/')||url.startsWith('/mcp/')){
return new Promise(function(resolve,reject){
var msgId='mcp_'+(Date.now())+'_'+Math.random().toString(36).slice(2,8);
function handler(e){
if(e.data&&e.data.type==='mcp-response'&&e.data.msgId===msgId){
window.removeEventListener('message',handler);
if(e.data.error)reject(new Error(e.data.error));
else resolve({ok:true,status:e.data.status,text:function(){return Promise.resolve(e.data.body)},json:function(){return Promise.resolve(JSON.parse(e.data.body))}});
}
}
window.addEventListener('message',handler);
window.parent.postMessage({type:'mcp-forward',msgId:msgId,serverId:sid,method:(o&&o.method)||'GET',path:url,body:o&&o.body||''},'*');
});
}
return origFetch.call(window,u,o);
};
})();
</script>`, serverID)
}

func isHTMLContent(contentType string) bool {
	return strings.Contains(contentType, "text/html")
}

type bridgeInjectionReader struct {
	reader   io.ReadCloser
	serverID string
	readBuf  []byte
	pos      int
}

func (b *bridgeInjectionReader) Read(p []byte) (n int, err error) {
	if b.readBuf == nil {
		data, err := io.ReadAll(b.reader)
		if err != nil {
			return 0, err
		}
		script := BridgeScript(b.serverID)
		bodyTag := "</body>"
		idx := strings.LastIndex(string(data), bodyTag)
		if idx >= 0 {
			b.readBuf = make([]byte, idx+len(script)+len(bodyTag))
			copy(b.readBuf, data[:idx])
			copy(b.readBuf[idx:], []byte(script))
			copy(b.readBuf[idx+len(script):], data[idx:])
		} else {
			b.readBuf = data
		}
	}
	if b.pos >= len(b.readBuf) {
		return 0, io.EOF
	}
	n = copy(p, b.readBuf[b.pos:])
	b.pos += n
	return n, nil
}

func (b *bridgeInjectionReader) Close() error {
	return b.reader.Close()
}

// Unregister 注销 MCP server 的 HTTP 服务
func (p *HTTPProxy) Unregister(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, id)
	p.rebuildMux()
}

func (p *HTTPProxy) rebuildMux() {
	p.mux = http.NewServeMux()
	for sid, targetURL := range p.routes {
		if t, err := url.Parse(targetURL); err == nil {
			prefix := "/mcp/" + sid + "/"
			proxy := httputil.NewSingleHostReverseProxy(t)
			proxy.ModifyResponse = func(r *http.Response) error {
				r.Header.Del("Content-Security-Policy")
				r.Header.Del("X-Frame-Options")
				return nil
			}
			p.mux.Handle(prefix, http.StripPrefix(prefix, proxy))
		}
	}
}

// ProxyURL 返回通过代理访问指定 MCP server 的入口 URL
func (p *HTTPProxy) ProxyURL(id string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.addr == "" {
		return ""
	}
	if _, ok := p.routes[id]; !ok {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1%s/mcp/%s/", p.addr, id)
}

// BaseURL 返回代理基础地址
func (p *HTTPProxy) BaseURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.addr == "" {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1%s", p.addr)
}

// Start 在随机端口启动 HTTP 代理
func (p *HTTPProxy) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("HTTP 代理启动失败: %w", err)
	}
	p.addr = fmt.Sprintf(":%d", listener.Addr().(*net.TCPAddr).Port)
	p.server = &http.Server{Handler: p.mux}
	go p.server.Serve(listener)
	log.Printf("[HTTPProxy] 已启动 http://127.0.0.1%s", p.addr)
	return nil
}

// Stop 停止 HTTP 代理
func (p *HTTPProxy) Stop() {
	if p.server != nil {
		p.server.Close()
	}
}

// Forward 通过 Go HTTP 客户端转发 API 请求，绕过 WebView2 fetch 限制
// 供 Wails IPC 调用: JS → Wails → Go Forward → MCP HTTP → 原路返回
func (p *HTTPProxy) Forward(ctx context.Context, serverID, method, path string, body []byte, headers map[string]string) (int, []byte, error) {
	p.mu.RLock()
	targetURL, ok := p.routes[serverID]
	p.mu.RUnlock()
	if !ok {
		return 0, nil, fmt.Errorf("未找到 MCP HTTP 服务: %s", serverID)
	}

	reqURL := strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(path, "/")
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("创建请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
