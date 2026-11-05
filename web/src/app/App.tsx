import { useEffect, useState } from 'react'
import { RouterProvider } from 'react-router'
import { MotionConfig } from 'motion/react'
import { session } from '../state/session'
import { createRouter } from './routes'

export function App() {
  const [router] = useState(createRouter)
  useEffect(() => {
    void session.bootstrap()
  }, [])
  return (
    // Every motion animation honours the system «Reduce motion» setting.
    <MotionConfig reducedMotion="user">
      <RouterProvider router={router} />
    </MotionConfig>
  )
}
