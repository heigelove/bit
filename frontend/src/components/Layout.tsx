import { NavLink, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'

export function Layout() {
  const { username, logout } = useAuth()

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <h1 className="brand">BIT</h1>
        <p className="brand-sub">ETHUSDT console</p>
        <nav className="nav">
          <NavLink to="/" end>
            账户
          </NavLink>
          <NavLink to="/trades">交易记录</NavLink>
          <NavLink to="/logs">日志</NavLink>
        </nav>
        <div className="sidebar-foot">
          <p className="mono muted" style={{ marginBottom: 10, fontSize: '0.78rem' }}>
            {username}
          </p>
          <button type="button" onClick={logout}>
            退出登录
          </button>
        </div>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  )
}
