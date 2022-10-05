module.exports = {
	target: "web",
	mode: "production",
	entry: "./src/index.ts",
	devtool: 'inline-source-map',
	output: {
		library: 'rpc',
		libraryTarget: 'umd'
	},
	module: {
		rules: [
			{
				test: /\.ts$/,
				include: /src/,
				exclude: /node_modules/,
				loader: "ts-loader"
			}
		]
	},
	resolve: {
		extensions: [".ts", ".js"]
	}
};
