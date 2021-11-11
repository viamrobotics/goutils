const webpack = require('webpack');
const path = require('path');

// see https://github.com/facebook/metro/issues/7#issuecomment-421072314
const installedDependencies = require("./package.json").dependencies;

const aliases = {};
Object.keys(installedDependencies).forEach(dep => {
	aliases[dep] = path.resolve(__dirname, "./node_modules", dep);
});
if ("proto" in aliases) {
	throw new Error("proto is already in aliases");
}
aliases["proto"] = path.resolve(__dirname, '../../../../dist/js/proto');
aliases["google-rpc"] = path.resolve(__dirname, '../../../../dist/js/google/rpc');
aliases["rpc"] = path.resolve(__dirname, '../../../js/src');

module.exports = {
	mode: "production",
	entry: "./src/index.ts",
	devtool: 'inline-source-map',
	module: {
		rules: [
			{
				test: /\.ts$/,
				include: /src|proto|google-rpc/,
				exclude: /node_modules/,
				loader: "ts-loader"
			}
		]
	},
	resolve: {
		alias: aliases,
		extensions: [".ts", ".js"]
	}
};
