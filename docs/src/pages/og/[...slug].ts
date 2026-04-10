import { getCollection } from 'astro:content';
import { OGImageRoute } from 'astro-og-canvas';

const entries = await getCollection('docs');
const pages = Object.fromEntries(entries.map(({ data, id }) => [id, { data }]));

export const { getStaticPaths, GET } = await OGImageRoute({
	pages,
	param: 'slug',
	getImageOptions: (_id, page: (typeof pages)[number]) => ({
		title: page.data.title,
		description: page.data.description,
		bgGradient: [[30, 22, 18], [45, 32, 24]],
		border: { color: [186, 120, 50], width: 20, side: 'block-end' },
		padding: 120,
		fonts: [
			'./node_modules/@fontsource/inter/files/inter-latin-400-normal.woff2',
			'./node_modules/@fontsource/inter/files/inter-latin-700-normal.woff2',
		],
	}),
});
