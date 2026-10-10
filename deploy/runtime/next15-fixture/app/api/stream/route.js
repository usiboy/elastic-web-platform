export const dynamic = 'force-dynamic';

export function GET() {
  const encoder = new TextEncoder();
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(encoder.encode('start\n'));
      setTimeout(() => {
        controller.enqueue(encoder.encode('done\n'));
        controller.close();
      }, 120);
    },
  });
  return new Response(stream, {
    headers: { 'content-type': 'text/plain; charset=utf-8', 'cache-control': 'no-store' },
  });
}
