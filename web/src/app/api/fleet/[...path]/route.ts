import { NextRequest, NextResponse } from 'next/server'

const controllerURL = process.env.FLEET_API_URL || 'http://localhost:8080'
const controllerOrigin = new URL(controllerURL).origin

// The dashboard has no sign-in of its own. This route forwards browser
// requests to the fleet controller carrying FLEET_API_TOKEN, a shared
// server-side credential, so every caller arrives at the controller as the
// same principal and the controller's role-based access control cannot
// distinguish them.
//
// Until the dashboard authenticates its users and mints per-user, role-scoped
// tokens, the proxy is read-only: state-changing methods are refused here
// rather than forwarded with a credential the caller never proved they hold.
//
// FLEET_DASHBOARD_ALLOW_MUTATIONS re-enables forwarding for demo and local
// development. Do not set it against a fleet you care about.
const READ_ONLY_METHODS = new Set(['GET', 'HEAD'])
const allowMutations = process.env.FLEET_DASHBOARD_ALLOW_MUTATIONS === 'true'

async function forward(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  if (!READ_ONLY_METHODS.has(request.method) && !allowMutations) {
    return NextResponse.json(
      {
        error: 'read-only dashboard',
        detail:
          'This dashboard forwards requests with a shared service credential and cannot ' +
          'attribute them to a user, so it does not perform state-changing operations. ' +
          'Use fleetctl or the fleet API directly with your own token.',
      },
      { status: 405 },
    )
  }

  const { path } = await context.params
  const sanitized = path.filter(segment => segment.length > 0 && segment !== '.' && segment !== '..')
  if (sanitized.length === 0 || sanitized.length !== path.filter(s => s.length > 0).length) {
    return NextResponse.json({ error: 'invalid path' }, { status: 400 })
  }
  const target = new URL(`/${sanitized.join('/')}`, controllerURL)
  if (target.origin !== controllerOrigin) {
    return NextResponse.json({ error: 'request blocked' }, { status: 400 })
  }
  target.search = request.nextUrl.search
  const headers = new Headers({ 'Content-Type': request.headers.get('content-type') || 'application/json' })
  if (process.env.FLEET_API_TOKEN) headers.set('Authorization', `Bearer ${process.env.FLEET_API_TOKEN}`)
  const body = READ_ONLY_METHODS.has(request.method) ? undefined : await request.arrayBuffer()
  try {
    const response = await fetch(target, { method: request.method, headers, body, cache: 'no-store' })
    return new NextResponse(response.body, { status: response.status, headers: { 'Content-Type': response.headers.get('content-type') || 'application/json' } })
  } catch {
    return NextResponse.json({ error: 'fleet controller is unavailable' }, { status: 503 })
  }
}

export const GET = forward
export const POST = forward
export const PUT = forward
export const PATCH = forward
export const DELETE = forward
