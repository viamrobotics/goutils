var path = require('path');

module.exports = {
	mode: "production",
	entry: "./src/index.ts",
	devtool: 'inline-source-map',
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
		extensions: [".ts", ".js"],
		alias: {
			"@viamrobotics/rpc": path.join(__dirname, "../../../js")
		}
	}
};
