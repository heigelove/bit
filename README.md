# BIT — ETHUSDT 永续交易系统

```
bit/
  backend/     Go 交易引擎 + REST API
  frontend/    React 数据展示后台
```

## 启动

### 1. Redis（账户缓存）

```bash
cd backend
docker compose up -d redis
```

### 2. 后端 API（给前端用）

```bash
cd backend
go run ./cmd/api -config configs/config.yaml
# 默认 :8080
```

默认登录：`admin` / `admin123`（见 `backend/configs/config.yaml` → `api`）

### 3. 交易引擎（可选，写 SQLite/Redis）

```bash
cd backend
go run ./cmd/trader -config configs/config.yaml
```

### 4. 前端

```bash
cd frontend
npm install
npm run dev
# http://localhost:5173  （/api 代理到 :8080）
```

## 功能

| 端 | 内容 |
|----|------|
| 前端登录 | Token 鉴权 |
| 账户页 | Redis 余额 / 持仓 / 风控 |
| 交易记录 | SQLite `trades` |
| 日志列表 | SQLite `app_logs` |

详见 `backend/README.md`、`frontend/README.md`。
