FROM nginx:alpine
COPY ui/nginx.conf /etc/nginx/templates/default.conf.template
COPY ui/index.html /usr/share/nginx/html/index.html

# UI_CORS_ALLOW_ORIGIN controls the Access-Control-Allow-Origin value for
# static-asset responses.  The nginx:alpine image automatically applies
# envsubst to files in /etc/nginx/templates/ at container startup.
#
# Override at deploy time to restrict to a known origin, e.g.:
#   UI_CORS_ALLOW_ORIGIN=https://ui.example.com
# The default "*" preserves backward compatibility for the static UI.
ENV UI_CORS_ALLOW_ORIGIN="*"
