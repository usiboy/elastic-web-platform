export const dynamic = 'force-dynamic';

export function GET() {
  return Response.json({ ok: true, service: 'r2-next-synthetic' }, { headers: { 'cache-control': 'no-store' } });
}
