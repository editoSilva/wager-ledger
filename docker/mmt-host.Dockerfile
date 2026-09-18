FROM alpine:3.20

RUN apk add --no-cache openssh-server bash curl jq \
    && ssh-keygen -A \
    && adduser -D -s /bin/bash tester \
    # adduser -D deixa a senha em "!" no /etc/shadow (conta bloqueada). O
    # OpenSSH do Alpine (sem PAM) rejeita QUALQUER auth, inclusive por chave
    # pública, para uma conta marcada como bloqueada — então mesmo só
    # aceitando SSH por chave, é preciso desbloquear a conta primeiro.
    && passwd -u tester

COPY docker/mmt-host-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22

ENTRYPOINT ["/entrypoint.sh"]
