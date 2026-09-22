FROM node:22-alpine
WORKDIR /app
# The package as it is in this checkout: the lab is where it meets a real MySQL, a real pool and
# real traffic, which is the only way to know that the origins it sends are the right lines.
COPY --from=slowpoke-node . /slowpoke-node
COPY package.json .
RUN npm install --omit=dev --no-audit --no-fund
COPY server.js .
CMD ["node", "server.js"]
