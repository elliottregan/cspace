// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeVintage from 'starlight-theme-vintage';

// https://astro.build/config
export default defineConfig({
	site: 'https://cspace-cli.netlify.app',
	integrations: [
		starlight({
			title: 'cspace',
			routeMiddleware: './src/routeData.ts',
			plugins: [starlightThemeVintage()],
			customCss: ['./src/styles/theme.css'],
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/elliottregan/cspace' }],
			sidebar: [
				{
					label: 'Getting Started',
					autogenerate: { directory: 'getting-started' },
				},
				{
					label: 'Configuration',
					autogenerate: { directory: 'configuration' },
				},
				{
					label: 'CLI Reference',
					autogenerate: { directory: 'cli-reference' },
				},
				{
					label: 'Features',
					autogenerate: { directory: 'features' },
				},
				{
					label: 'Architecture',
					autogenerate: { directory: 'architecture' },
				},
				{
					label: 'Skills & Workflows',
					autogenerate: { directory: 'skills-and-workflows' },
				},
			],
		}),
	],
});
