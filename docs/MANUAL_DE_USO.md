# Manual de Uso

Este manual descreve como usar o projeto tbc para autenticar no TeraBox, gerenciar perfis de conta e montar o armazenamento como drive no Linux.

## Visão Geral

O tbc é um cliente de linha de comando escrito em Go com suporte a:

- autenticação por cookie e login assistido
- perfis de múltiplas contas
- listagem, upload, download e operações de arquivos
- montagem do TeraBox como filesystem FUSE
- modo leitura e leitura/escrita
- automação de montagem, watchdog e integrações locais

## Requisitos

- Linux com FUSE disponível
- Go 1.24 ou superior para compilar do código-fonte
- acesso a uma conta TeraBox válida

Para os comandos de montagem, normalmente é necessário ter um dos binários abaixo no sistema:

- fusermount3
- fusermount

## Estrutura Padrão

O projeto usa estes caminhos padrão no diretório home:

- cookie padrão: ~/.config/tbc/terabox.cookie
- perfis de conta: ~/.config/tbc/accounts
- raiz de montagem: ~/TeraBox
- montagem por conta: ~/TeraBox/accounts
- montagem unificada: ~/TeraBox/union

## Compilação

Na raiz do projeto:

```bash
go build -o tbc ./cmd/tbc
```

Se quiser rodar sem instalar:

```bash
./tbc --help
```

## Autenticação

O tbc aceita cookie por variável de ambiente, arquivo ou perfil salvo.

### 1. Variável de ambiente

```bash
export TERABOX_COOKIE='ndus=SEU_COOKIE; ...'
./tbc ls
```

### 2. Arquivo de cookie

```bash
./tbc --cookie-file ~/.config/tbc/terabox.cookie ls
```

### 3. Login guiado

O comando login salva o cookie e valida a conta.

```bash
./tbc login
```

Opções úteis:

- salvar em um perfil: ./tbc login --account pessoal
- salvar em caminho específico: ./tbc login --output /caminho/cookie.txt
- usar servidor web local: ./tbc login --web
- colar cookie manualmente: ./tbc login --manual
- definir timeout: ./tbc login --timeout 10m
- apontar navegador: ./tbc login --browser /usr/bin/google-chrome

Em ambiente headless ou VPS, prefira:

```bash
./tbc login --web --account servidor
```

## Perfis de Conta

Listar perfis salvos:

```bash
./tbc accounts
```

Usar um perfil salvo em qualquer comando:

```bash
./tbc --account pessoal ls
./tbc --account trabalho df
```

## Operações Básicas

Os comandos abaixo fazem parte do fluxo principal do cliente:

- ls: listar arquivos
- mv: mover arquivos ou diretórios remotos
- cp: copiar arquivos ou diretórios remotos
- rm: remover arquivos ou diretórios remotos
- mkdir: criar diretório remoto
- find: buscar arquivos
- info: mostrar dados da conta
- put: enviar arquivo
- get: baixar arquivo
- df: mostrar uso de espaço

Exemplos:

```bash
./tbc ls /
./tbc find / filme
./tbc mkdir /Backups
./tbc put ./video.mp4 /Uploads
./tbc get /Uploads/video.mp4
./tbc df
```

## Montando o TeraBox como Drive

Existem dois níveis de uso: comandos diretos de montagem e comandos gerenciados.

### Comandos Diretos

Montagem somente leitura:

```bash
mkdir -p /tmp/terabox-ro
./tbc mount-ro /tmp/terabox-ro
```

Montagem com escrita:

```bash
mkdir -p /tmp/terabox-rw
./tbc mount-rw /tmp/terabox-rw
```

Opções de modo:

- --mode prefixed: separa por conta
- --mode union: junta o conteúdo em uma visão unificada

Exemplo em modo union:

```bash
./tbc mount-ro --mode union /tmp/terabox-union
```

Em modo de escrita com conta de destino padrão:

```bash
./tbc mount-rw --mode union --dest-account pessoal /tmp/terabox-union
```

### Comandos Gerenciados

O projeto também oferece um fluxo pronto para criar as montagens fixas no home.

Subir as montagens padrão:

```bash
./tbc up
```

Subir com escrita:

```bash
./tbc up --rw
```

Subir com escrita definindo a conta padrão da visão union:

```bash
./tbc up --rw --dest-account pessoal
```

Desmontar os pontos padrão:

```bash
./tbc down
```

Abrir no gerenciador de arquivos:

```bash
./tbc open
```

Os caminhos usados por esses comandos são:

- ~/TeraBox/accounts
- ~/TeraBox/union

## Watchdog e Serviços

O projeto inclui comandos para automação de montagem e integração com serviços locais.

Comandos disponíveis no código:

- watchdog
- install-service
- remove-service
- install-launcher
- remove-launcher
- install-watchdog
- remove-watchdog

Uso típico do watchdog:

```bash
./tbc watchdog --dest-account pessoal
```

Esse modo verifica se os mounts caíram e tenta remontar automaticamente.

## Outros Comandos Disponíveis

Além do núcleo de arquivos e mount, o projeto já registra estes comandos adicionais:

- ui
- quota-all
- union-ls
- sync
- xcp
- config
- share
- trash
- stream
- vault
- status-server
- autosync
- completion

Como o conjunto do projeto ainda está em evolução, alguns desses comandos podem depender de configuração extra ou estar em desenvolvimento.

## Fluxos Recomendados

### Uso simples com uma conta

```bash
./tbc login --account pessoal
./tbc --account pessoal ls /
./tbc up --rw --dest-account pessoal
xdg-open ~/TeraBox/union
```

### Uso em VPS ou máquina remota

```bash
./tbc login --web --account servidor
./tbc up
./tbc watchdog --dest-account servidor
```

### Uso com múltiplas contas

```bash
./tbc login --account pessoal
./tbc login --account trabalho
./tbc up --rw --dest-account pessoal
```

Nesse caso:

- ~/TeraBox/accounts mostra o conteúdo separado por perfil
- ~/TeraBox/union mostra a visão unificada

## Solução de Problemas

### O mount não sobe

Verifique:

- se o FUSE está instalado
- se fusermount3 ou fusermount existe no PATH
- se há pelo menos um perfil salvo em ~/.config/tbc/accounts
- se o cookie ainda é válido

### Não há navegador gráfico

Use:

```bash
./tbc login --web
```

ou:

```bash
./tbc login --manual
```

### Quais contas estão salvas

```bash
./tbc accounts
```

### Qual conta será usada em escrita no modo union

Defina explicitamente:

```bash
./tbc mount-rw --mode union --dest-account pessoal /seu/mountpoint
```

ou:

```bash
./tbc up --rw --dest-account pessoal
```

## Observações

Este manual foi escrito com base na estrutura atual do código-fonte e nos comandos registrados no aplicativo. Se novos comandos forem adicionados, atualize este arquivo junto com as mudanças do projeto.