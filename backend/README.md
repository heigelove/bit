# BIT Backend

Go 服务：币安 ETHUSDT 永续趋势交易 + **Gin** 管理 API。

## 命令

```bash
# 交易引擎（写 SQLite / Redis）
go run ./cmd/trader -config configs/config.yaml

# 管理 API（Gin，供前端）
go run ./cmd/api -config configs/config.yaml
```

## 配置要点

- `sqlite.path`：日志 / 信号 / 成交
- `redis.*`：账户余额、持仓、风控
- `api.*`：监听地址与登录账号

默认 API：`http://127.0.0.1:8080`  
默认登录：`admin` / `admin123`

## API

| Method | Path | Auth | 说明 |
|--------|------|------|------|
| POST | `/api/auth/login` | 否 | 登录 |
| GET | `/api/auth/me` | 是 | 当前用户 |
| GET | `/api/account` | 是 | 账户/持仓/风控 |
| GET | `/api/logs` | 是 | 日志分页 |
| GET | `/api/trades` | 是 | 成交分页 |
| GET | `/api/signals` | 是 | 信号分页 |
| GET | `/api/health` | 否 | 健康检查 |
