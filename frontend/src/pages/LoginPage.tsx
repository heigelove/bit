import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'

export function LoginPage() {
  const { username, loading, login } = useAuth()
  const navigate = useNavigate()
  const [user, setUser] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  if (!loading && username) return <Navigate to="/" replace />

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      await login(user, password)
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login-page">
      <form className="panel login-card" onSubmit={onSubmit}>
        <h1>BIT Console</h1>
        <p className="lead">登录查看账户、成交与运行日志</p>
        <div className="field">
          <label htmlFor="username">用户名</label>
          <input
            id="username"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            autoComplete="username"
            required
          />
        </div>
        <div className="field">
          <label htmlFor="password">密码</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </div>
        {error ? <p className="error-box" style={{ padding: '8px 0' }}>{error}</p> : null}
        <button className="btn btn-primary" type="submit" disabled={submitting}>
          {submitting ? '登录中…' : '进入后台'}
        </button>
      </form>
    </div>
  )
}
