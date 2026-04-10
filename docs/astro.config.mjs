// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeVintage from 'starlight-theme-vintage';

// https://astro.build/config
export default defineConfig({
	integrations: [
		starlight({
			title: 'cspace',
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
