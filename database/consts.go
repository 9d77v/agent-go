package database

// SQLite DSN 参数常量。
const (
	// sqliteWALParams 启用 WAL 并设置 5s busy timeout 的 DSN 参数。
	sqliteWALParams = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	// sqliteReadOnlyMode 只读连接的 DSN 参数。
	sqliteReadOnlyMode = "mode=ro"
)

// maxTitleRunes 会话标题（从首条用户消息截取）的最大字符数。
const maxTitleRunes = 50
