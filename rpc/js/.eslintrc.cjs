'use strict';

module.exports = {
  root: true,
  extends: ['@viamrobotics/eslint-config'],
  parserOptions: {
    tsconfigRootDir: __dirname,
    project: './tsconfig.json',
  },
};
