wavesurfer.js 7.12.12 (BSD-3-Clause, see LICENSE), from the npm package
`wavesurfer.js@7.12.12`, files `dist/wavesurfer.esm.js` and
`dist/plugins/{regions,timeline,minimap,hover}.esm.js`, unmodified.
They are self-contained ES modules, so no build step is needed.

The `.d.ts` files beside them are ours: they give the check of the JavaScript
(`scripts/test.sh js`) the package's types, from the same version in
`web/e2e/package.json`. Keep the two versions equal.
