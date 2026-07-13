import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { Layout } from './Layout'
import { LoginPage } from './pages/LoginPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { ProjectWorkspace } from './pages/ProjectWorkspace'
import { SearchPage } from './pages/SearchPage'
import { ChatPage } from './pages/ChatPage'
import { CliGuidePage } from './pages/CliGuidePage'
import { SettingsPage } from './pages/SettingsPage'
import { JobsPage } from './pages/JobsPage'

function Private({ children }: Readonly<{ children: React.ReactNode }>) {
  const { user, loading } = useAuth()
  if (loading) {
    return (
      <div className="centered">
        <p className="muted">Loading…</p>
      </div>
    )
  }
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          path="/*"
          element={
            <Private>
              <Layout>
                <Routes>
                  <Route path="/" element={<ProjectsPage />} />
                  <Route path="/projects/:name" element={<ProjectWorkspace />} />
                  <Route path="/search" element={<SearchPage />} />
                  <Route path="/chat" element={<ChatPage />} />
                  <Route path="/jobs" element={<JobsPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  <Route path="/cli" element={<CliGuidePage />} />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </Layout>
            </Private>
          }
        />
      </Routes>
    </AuthProvider>
  )
}
