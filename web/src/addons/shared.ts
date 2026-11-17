// The modules an addon bundle imports from the app instead of bundling its own
// copy (one React, one router): globalThis.__antresol.shared[<specifier>].
import {
  Activity,
  Children,
  Component,
  Fragment as ReactFragment,
  Profiler,
  PureComponent,
  StrictMode,
  Suspense,
  cloneElement,
  createContext,
  createElement,
  createRef,
  forwardRef,
  isValidElement,
  lazy,
  memo,
  startTransition,
  use,
  useActionState,
  useCallback,
  useContext,
  useDebugValue,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useId,
  useImperativeHandle,
  useInsertionEffect,
  useLayoutEffect,
  useMemo,
  useOptimistic,
  useReducer,
  useRef,
  useState,
  useSyncExternalStore,
  useTransition,
  version,
} from 'react'
import { Fragment, jsx, jsxs } from 'react/jsx-runtime'
import { createPortal } from 'react-dom'
// Named, not namespaces: a namespace keeps every export of the library in the
// app's first load. These are what the app itself already ships.
import { Link, NavLink, Navigate, Outlet, useLocation, useNavigate, useNavigationType, useParams, useSearchParams } from 'react-router'
import * as sdk from './sdk'
import { registerAddon } from './registry'

export type AntresolGlobal = {
  shared: Record<string, unknown>
  register: typeof registerAddon
}

declare global {
  var __antresol: AntresolGlobal | undefined
}

/** Puts the shared modules on globalThis (once; before any addon bundle loads). */
export function installShared() {
  if (globalThis.__antresol) return
  globalThis.__antresol = {
    shared: {
      // React's stable public API (addon-sdk/vite-plugin.mjs SHARED.react)
      react: {
        Activity,
        Children,
        Component,
        Fragment: ReactFragment,
        Profiler,
        PureComponent,
        StrictMode,
        Suspense,
        cloneElement,
        createContext,
        createElement,
        createRef,
        forwardRef,
        isValidElement,
        lazy,
        memo,
        startTransition,
        use,
        useActionState,
        useCallback,
        useContext,
        useDebugValue,
        useDeferredValue,
        useEffect,
        useEffectEvent,
        useId,
        useImperativeHandle,
        useInsertionEffect,
        useLayoutEffect,
        useMemo,
        useOptimistic,
        useReducer,
        useRef,
        useState,
        useSyncExternalStore,
        useTransition,
        version,
      },
      'react/jsx-runtime': { Fragment, jsx, jsxs },
      'react-dom': { createPortal },
      'react-router': { Link, NavLink, Navigate, Outlet, useLocation, useNavigate, useNavigationType, useParams, useSearchParams },
      '@antresol/addon-sdk': sdk,
    },
    register: registerAddon,
  }
}
