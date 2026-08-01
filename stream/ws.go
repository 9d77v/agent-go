package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// MessageHandler is a function that handles a custom WebSocket message.
// Returns true if the message was handled (prevents default dispatch).
type MessageHandler func(conn *websocket.Conn, req *WsRequest) bool

// WSServer provides a WebSocket server for agent streaming communication.
// It handles /ws (main agent stream) and /files/{idx}/{path} (file serving) routes.
// Application-specific routes can be registered via RegisterRoute before Start().
type WSServer struct {
	port           int
	server         *http.Server
	sm             *StreamManager
	mu             sync.Mutex
	clients        map[*websocket.Conn]bool
	fileServerDirs []string
	onMessage      MessageHandler // optional: hook for custom message types
	routes         []routeEntry   // custom routes registered by application layer
}

type routeEntry struct {
	pattern string
	handler http.HandlerFunc
}

// RegisterRoute adds a custom HTTP route to the server mux.
// Must be called before Start(). The pattern follows http.ServeMux conventions.
func (ws *WSServer) RegisterRoute(pattern string, handler http.HandlerFunc) {
	ws.routes = append(ws.routes, routeEntry{pattern, handler})
}

// SetOnMessage registers a custom message handler hook.
// Called before the default dispatch; if it returns true, the message is considered handled.
func (ws *WSServer) SetOnMessage(h MessageHandler) {
	ws.onMessage = h
}

// NewWSServer creates a new WebSocket server.
func NewWSServer(sm *StreamManager, fileDirs ...string) *WSServer {
	return &WSServer{
		sm:             sm,
		clients:        make(map[*websocket.Conn]bool),
		fileServerDirs: fileDirs,
	}
}

// AddFileServerDir adds a directory for file serving.
func (ws *WSServer) AddFileServerDir(dir string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if slices.Contains(ws.fileServerDirs, dir) {
		return
	}
	ws.fileServerDirs = append(ws.fileServerDirs, dir)
}

// ClearFileServerDirs clears all file server directories.
func (ws *WSServer) ClearFileServerDirs() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.fileServerDirs = nil
}

// BaseURL returns the HTTP server base URL.
func (ws *WSServer) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", ws.port)
}

// Port returns the server port number.
func (ws *WSServer) Port() int { return ws.port }

// ServeWS handles WebSocket upgrade and message loop.
func (ws *WSServer) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws.handleWS(w, r)
}

// ServeFile handles file serving for /files/{idx}/{path...}.
func (ws *WSServer) ServeFile(w http.ResponseWriter, r *http.Request) {
	ws.mu.Lock()
	dirs := make([]string, len(ws.fileServerDirs))
	copy(dirs, ws.fileServerDirs)
	ws.mu.Unlock()

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/files/"), "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	var idx int
	if _, err := fmt.Sscanf(parts[0], "%d", &idx); err != nil || idx < 0 || idx >= len(dirs) {
		http.NotFound(w, r)
		return
	}
	safePath := filepath.Clean(parts[1])
	if strings.Contains(safePath, "..") {
		http.Error(w, "Forbidden", 403)
		return
	}
	fullPath := filepath.Join(dirs[idx], safePath)
	if !strings.HasPrefix(fullPath, filepath.Clean(dirs[idx])) {
		http.Error(w, "Forbidden", 403)
		return
	}
	http.ServeFile(w, r, fullPath)
}

// Start starts the WebSocket server on a random localhost port.
// Custom routes registered via RegisterRoute() are included in the mux.
func (ws *WSServer) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	ws.port = listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", ws.ServeWS)
	mux.HandleFunc("/files/", ws.ServeFile)
	// Custom routes registered by the application layer
	for _, r := range ws.routes {
		mux.HandleFunc(r.pattern, r.handler)
	}

	ws.server = &http.Server{Handler: mux}
	go func() {
		if err := ws.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[WS] server error: %v", err)
		}
	}()
	return nil
}

// Stop stops the server.
func (ws *WSServer) Stop() {
	if ws.server != nil {
		ws.server.Shutdown(context.Background())
	}
}

// WriteJSON sends a StreamMessage to a WebSocket connection.
// This is a public wrapper around writeJSON for use by application-layer code
// (e.g., custom message handlers registered via SetOnMessage).
func (ws *WSServer) WriteJSON(c *websocket.Conn, msg StreamMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return ws.writeJSON(c, data)
}

// Broadcast sends a JSON message to all connected clients.
// data can be any JSON-serializable value; it will be marshaled internally.
func (ws *WSServer) Broadcast(data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("[WS] broadcast marshal error: %v", err)
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for c := range ws.clients {
		_ = ws.writeJSON(c, raw)
	}
}

// writeJSON sends JSON-encoded bytes to a WebSocket connection.
func (ws *WSServer) writeJSON(c *websocket.Conn, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, data)
}

// handleWS handles the main WebSocket connection.
func (ws *WSServer) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[WS] accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ws.mu.Lock()
	ws.clients[c] = true
	ws.mu.Unlock()
	defer func() {
		ws.mu.Lock()
		delete(ws.clients, c)
		ws.mu.Unlock()
	}()

	// Read loop
	for {
		_, msg, err := c.Read(context.Background())
		if err != nil {
			break
		}
		var req WsRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			log.Printf("[WS] invalid message: %v", err)
			continue
		}
		ws.handleMessage(c, &req)
	}
}

// handleMessage dispatches a WebSocket request.
func (ws *WSServer) handleMessage(c *websocket.Conn, req *WsRequest) {
	// Custom handler hook (registered by application layer)
	if ws.onMessage != nil && ws.onMessage(c, req) {
		return
	}
	switch req.Type {
	case "start":
		ws.handleStart(c, req)
	case "cancel":
		ws.sm.CancelStream(req.StreamID)
	case "approve_tool":
		ws.sm.ResolveApproval(req.ApprovalID, true)
	case "reject_tool":
		ws.sm.ResolveApproval(req.ApprovalID, false)
	case "questionnaire_answer":
		ws.sm.ResolveQuestionnaire(req.QuestionnaireID, req.Text)
	case "continue_response":
		ws.handleContinue(c, req)
	case "ping":
		// keep-alive, no response needed
	default:
		log.Printf("[WS] unknown message type: %s", req.Type)
	}
}

// handleStart starts a new stream and sends messages to the WebSocket.
// Supports optional OnClientDisconnect hook for cleanup.
func (ws *WSServer) handleStart(c *websocket.Conn, req *WsRequest) {
	ws.startStream(c, req, req.Message)
}

// handleContinue resumes the agent on the same session.
// 前端在达到最大迭代次数后点击"继续执行"时调用，复用同一 session 继续编排。
func (ws *WSServer) handleContinue(c *websocket.Conn, req *WsRequest) {
	if req.SessionID == "" {
		ws.writeJSON(c, mustMarshal(StreamMessage{Type: "error", Error: "session_id required for continue"}))
		return
	}
	ws.startStream(c, req, "（自动继续）请从上次中断的地方继续完成你的任务。")
}

// startStream opens a stream for the given message and forwards messages to the client.
func (ws *WSServer) startStream(c *websocket.Conn, req *WsRequest, message string) {
	// 前端未传 sessionID 时由后端生成 UUID v7
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.Must(uuid.NewV7()).String()
	}

	streamID, msgCh, err := ws.sm.StartStream(
		sessionID, message, req.Images, req.Model, req.ProviderID,
		req.Mode, req.Thinking, req.ApprovalMode, req.IncludeProjectDocs,
	)
	if err != nil {
		ws.writeJSON(c, mustMarshal(StreamMessage{Type: "error", Error: err.Error()}))
		return
	}

	// Send started event with sessionID
	ws.writeJSON(c, mustMarshal(StreamMessage{Type: "started", StreamID: streamID, SessionID: sessionID}))

	for msg := range msgCh {
		msg.StreamID = streamID
		data, _ := json.Marshal(msg)
		if err := ws.writeJSON(c, data); err != nil {
			log.Printf("[WS] 客户端断连 (stream=%s): %v，取消会话", streamID, err)
			ws.sm.CancelStream(streamID)
			break
		}
	}
	log.Printf("[WS] 流完成 (stream=%s)", streamID)
}

func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
