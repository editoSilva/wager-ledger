# Fluxo Git

`main` é a linha de integração. Uma branch curta por alteração, revisada por PR:

| Trabalho | Branch | Commit |
| --- | --- | --- |
| Funcionalidade | `feature/<assunto>` | `feat(escopo): descrição` |
| Correção comum | `bugfix/<assunto>` | `fix(escopo): descrição` |
| Incidente em produção | `hotfix/<assunto>` | `fix(escopo): descrição` |
| Documentação / testes / automação | branch coerente com a tarefa | `docs`, `test`, `ci`, `build`, `chore` |

Hotfix descreve urgência e origem da branch, não um tipo inventado de Conventional
Commits. Crie a branch a partir do commit efetivamente implantado se `main` já tiver
mudanças não publicadas. Integre a correção de volta por PR; não perca a correção
na próxima release. O pipeline atual implanta somente `main`; hotfix também passa
por revisão e integração antes do deploy, sem ignorar a CI.

O agente `commit-manager` revisa o diff, executa verificações, adiciona caminhos
explícitos e cria commits locais por unidade de mudança. O autor é a identidade
Git configurada pelo usuário. Publicação no remote é uma etapa separada.

Exemplo:

```sh
git switch -c feature/ledger-paginado
# implementar e testar
git add internal/infra/http/ledger.go
git diff --cached
git commit -m 'feat(ledger): adiciona consulta paginada'
```

Configure no GitHub a proteção de `main`: exigir PR, revisão e o check de teste
exibido pela CI; bloquear force-push e exclusão. Isso é configuração do repositório,
não algo aplicado apenas pela presença destes arquivos. CI executa em PRs e pushes
de `feature/**`, `bugfix/**` e `hotfix/**`; a entrega de `main` chama a mesma CI.

Este diretório não tinha `.git` na análise inicial de 2026-09-17. O primeiro commit
registra o snapshot recebido, sem tentar reconstruir datas ou commits históricos.
As mudanças seguintes têm commits próprios. Não se cria hotfix fictício para
classificar trabalho de documentação ou configuração como incidente.
