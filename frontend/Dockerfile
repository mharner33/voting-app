FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/public/ /usr/share/nginx/html/
COPY frontend/gen-config.sh /gen-config.sh
COPY frontend/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /gen-config.sh /docker-entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]
