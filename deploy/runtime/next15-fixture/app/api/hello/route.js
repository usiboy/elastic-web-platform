export const dynamic = 'force-dynamic';

export function GET() {
  const result = { service: 'r2-next-synthetic', node: process.versions.node };
  if (process.env.EWP_SYNTHETIC_VARIANT === 'v3') result.variant = 'v3';
  return Response.json(result);
}
