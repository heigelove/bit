# BIT Frontend

React + Vite 数据后台：登录、账户余额、交易记录、运行日志。

```bash
npm install
npm run dev
```

开发服务器将 `/api` 代理到 `http://127.0.0.1:8080`。请先启动 `backend` 的 API：

```bash
cd ../backend
go run ./cmd/api -config configs/config.yaml
```

默认账号：`admin` / `admin123`
