import { randomUUID } from 'node:crypto';

export const dynamic = 'force-dynamic';

export default function Home() {
  return (
    <main>
      <h1>R2 合成 SSR</h1>
      <p>此页面由 Node 运行时在请求时渲染，不包含真实业务数据。</p>
      <p data-request-id={randomUUID()}>合成请求</p>
    </main>
  );
}
