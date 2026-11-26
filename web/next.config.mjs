import { fileURLToPath } from "node:url";

// Exportação estática: `NEXT_EXPORT=1 next build` gera um diretório totalmente estático em web/out, que pode ser colocado direto na
// raiz web do nginx. Em desenvolvimento (next dev) essa variável não é definida, mantendo o proxy reverso de /api e o hot reload.
const isExport = process.env.NEXT_EXPORT === "1";
// Demo na Vercel: o site inteiro usa mock, sem backend e sem precisar do proxy reverso de /api.
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Evita que o lockfile de diretórios pais afete a inferência do diretório raiz e a geração dos caminhos de recursos.
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // Permite acessar recursos de dev (HMR) a partir de IPs da rede local; adicione ou remova conforme necessário.
  // Na fase de dev, libera qualquer origem IPv4 para acessar /_next/* e o HMR (mudanças de IP na rede local também não afetam).
  // Atenção: por segurança, o Next proíbe o "*" puro, sendo necessário usar curinga por segmento; "*.*.*.*" corresponde a qualquer IPv4.
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // Exportação totalmente estática: sem runtime Node; imagens sem otimização; cada rota gera <route>/index.html.
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Demo mock na Vercel: sem backend, não precisa do proxy reverso de /api.
          images: { unoptimized: true },
        }
      : {
          // Desenvolvimento: encaminha /api/* para o backend Go (padrão :8787, pode ser sobrescrito com AUTOPENTEST_API).
          async rewrites() {
            const backend = process.env.AUTOPENTEST_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

export default nextConfig;
