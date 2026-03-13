#\!/usr/bin/env /Users/joelschaeffer/.bun/bin/bun
const chunks: Buffer[] = [];
for await (const chunk of Bun.stdin.stream()) { chunks.push(Buffer.from(chunk)); }
console.log(JSON.stringify({}));
