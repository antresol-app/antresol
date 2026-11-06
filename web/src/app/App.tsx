import { useEffect, useState } from 'react'
import { RouterProvider } from 'react-router'
import { session } from '../state/session'
import { createRouter } from './routes'

export function App() {
  const [router] = useState(createRouter)
  useEffect(() => {
    void session.bootstrap()
  }, [])
  return <RouterProvider router={router} />
}
