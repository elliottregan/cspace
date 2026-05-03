import { $ } from "bun";

await $`bun build src/main.ts --compile --target=bun-linux-arm64 --outfile=dist/cspace-supervisor`;
console.log("Built dist/cspace-supervisor");
