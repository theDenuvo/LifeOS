import { sveltekit } from '@sveltejs/kit/vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';
import { VitePWA } from '@vite-pwa/sveltekit';

export default defineConfig({
	plugins: [
		svelte(),
		sveltekit(),
		VitePWA({
			registerType: 'autoUpdate',
			includeAssets: ['favicon.ico', 'robots.txt'],
			manifest: {
				name: 'LifeOS',
				short_name: 'LifeOS',
				description: 'Personal Operating System with Intelligent Assistant',
				theme_color: '#8b5cf6',
				background_color: '#0a0a0f',
				display: 'standalone',
				scope: '/',
				start_url: '/',
				icons: [
					{
						src: '/icon-192.png',
						sizes: '192x192',
						type: 'image/png',
						purpose: 'any maskable'
					},
					{
						src: '/icon-512.png',
						sizes: '512x512',
						type: 'image/png',
						purpose: 'any maskable'
					}
				]
			},
			workbox: {
				globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],
				runtimeCaching: [
					{
						urlPattern: /^https:\/\/api\./,
						handler: 'NetworkFirst',
						options: {
							cacheName: 'api-cache',
							expiration: {
								maxEntries: 100,
								maxAgeSeconds: 60 * 60 * 24
							}
						}
					}
				]
			}
		})
	],
	server: {
		port: 5173,
		proxy: {
			'/api': {
				target: 'http://127.0.0.1:8787',
				changeOrigin: true
			},
			'/healthz': {
				target: 'http://127.0.0.1:8787',
				changeOrigin: true
			}
		}
	}
});