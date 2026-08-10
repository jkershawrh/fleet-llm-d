import { NextRequest, NextResponse } from 'next/server'

const controllerURL = process.env.FLEET_API_URL || 'http://localhost:8080'
const controllerOrigin = new URL(controllerURL).origin

async function forward(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params
  const sanitized = path.filter(segment => segment.length > 0)
  if (sanitized.length === 0) {
    return NextResponse.json({ error: 'invalid path' }, { status: 400 })
  }
  const target = new URL(`/${sanitized.join('/')}`, controllerURL)
  if (target.origin !== controllerOrigin) {
    return NextResponse.json({ error: 'request blocked' }, { status: 400 })
  }
  target.search = request.nextUrl.search
  const headers = new Headers({ 'Content-Type': request.headers.get('content-type') || 'application/json' })
  if (process.env.FLEET_API_TOKEN) headers.set('Authorization', `Bearer ${process.env.FLEET_API_TOKEN}`)
  const body = request.method === 'GET' || request.method === 'HEAD' ? undefined : await request.arrayBuffer()
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
