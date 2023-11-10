const path = require('node:path');

module.exports = {
  mode: 'production',
  entry: './src/index.ts',
  devtool: 'inline-source-map',
  output: {
    libraryTarget: 'module',
  },
  module: {
    rules: [
      {
        test: /\.ts$/,
        loader: 'ts-loader',
        options: {
          configFile: path.join(__dirname, './tsconfig.build.json'),
        },
      },
    ],
  },
  resolve: {
    extensions: ['.ts', '.js'],
  },
  experiments: {
    outputModule: true,
  },
};
