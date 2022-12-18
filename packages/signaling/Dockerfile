FROM node:16
WORKDIR /app
COPY package-lock.json .
COPY package.json .
RUN npm ci

COPY . .
EXPOSE 3000
CMD [ "npm", "run", "start:prod" ]

