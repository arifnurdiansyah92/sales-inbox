import type { NextConfig } from 'next'

const nextConfig: NextConfig = {
  output: 'standalone',
  basePath: process.env.BASEPATH,
  redirects: async () => {
    return [
      {
        source: '/',
        destination: '/inbox',
        permanent: true,
        locale: false
      }
    ]
  }
}

export default nextConfig
