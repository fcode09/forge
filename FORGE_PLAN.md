# FORGE — Local Go CI/CD Deployment Platform
## Plan de desarrollo v1.1

**Nombre:** FORGE  
**Tagline:** Build local. Ship securely. Deploy reliably.  
**Versión del plan:** 1.1 (integración con `itc-deploy`)  
**Lenguaje:** Go  
**Objetivo:** herramienta de CI/CD self-hosted, sin servicios SaaS. Se ejecuta desde la carpeta de un proyecto y **construye, empaqueta y transfiere** imágenes Docker a un servidor a través de una VPN privada. El **despliegue en el servidor lo hace `itc-deploy` (`deltactl`)**, que FORGE dispara por SSH.

> **Cambios respecto a v1.0:** se elimina `forge-agent`. Compose, healthchecks, historial de despliegues y rollback ya los resuelve `deltactl` (repo `itc-deploy`); FORGE no los reimplementa. Ver secciones 2, 8, 9 y 10 y los milestones M5–M7.

---

## 1. Visión

FORGE es una herramienta CLI en Go que automatiza el tramo "del código a la imagen cargada en el servidor" y lo conecta con `deltactl`.

Desde la raíz del proyecto:

```bash
forge build
forge deploy
forge rollback
```

La primera versión prioriza ejecución explícita desde la ubicación del proyecto (modo local), con un servidor de producción y comunicación por VPN. Más adelante se puede añadir un servidor CI dedicado que ejecute los mismos comandos.

**Principios:**
- Todo el código propio de FORGE en Go. **FORGE no escribe Bash**: usa el `deltactl` ya existente como ejecutor remoto, con argv fijo y sin shell.
- No depender de GitLab Cloud, GitHub Actions, Docker Hub ni un registry SaaS.
- Docker Engine/BuildKit invocados desde Go con subprocesos controlados (argv, sin shell).
- Transferencia de artefactos por SSH/SFTP sobre VPN.
- Configuración declarativa y validada.
- Versiones reproducibles, checksums y logs.
- No tocar volúmenes persistentes ni datos de bases de datos (lo garantiza `deltactl`).

## 2. Arquitectura objetivo

```text
FORGE CLI (Go) — máquina de desarrollo o servidor CI
  ├── Config loader + validator
  ├── Git metadata / release-id
  ├── Docker build + tests
  ├── Image export (docker save + gzip)
  ├── SHA-256 manifest
  ├── SSH/SFTP transport (VPN)
  └── Remote executor (lista cerrada de comandos deltactl)
        |
        | WireGuard network + SSH
        v
Servidor de producción (Ubuntu) — repo itc-deploy
  ├── /srv/forge/incoming/<release-id>/   (staging, validado con sha256sum -c)
  └── deltactl
        ├── load       (docker load)
        ├── env set    (S0_TAG / S1_TAG)
        ├── check      (validación sin levantar nada)
        ├── deploy     (Compose + espera a healthy + guarda etiquetas previas)
        └── rollback   (vuelve a las etiquetas previas)
```

### Frontera FORGE / itc-deploy

| FORGE (este repo) | `itc-deploy` / `deltactl` (no se modifica desde aquí) |
|---|---|
| `forge.yaml`, `doctor` local | Compose, redes, puertos y secretos por instancia |
| Build con tags inmutables `<imagen>:<release-id>` | Cargar la imagen y validar la instancia |
| Tests, export, manifest y SHA-256 | Levantar y esperar `healthy` |
| Transferencia y validación del checksum | Guardar etiquetas previas y hacer rollback |
| Disparar `deltactl` con una lista cerrada de comandos | Historial y bloqueo de operaciones (`runs`) |

`itc-deploy` empieza "cuando la imagen ya está en el Docker del servidor"; FORGE termina de dejarla allí y lo invoca.

### Fase inicial (v1.0)
El CLI se ejecuta desde la máquina de desarrollo o el servidor CI, dentro del proyecto del backend (no dentro de `itc-deploy`).

### Evolución (v1.x)
Un servidor CI dedicado puede clonar repositorios y ejecutar `forge deploy` por webhook o Git hook, reutilizando el mismo CLI y formato de artefactos.

## 3. Stack

- **Lenguaje:** Go (la versión instalada al iniciar: 1.27.x).
- **CLI:** biblioteca estándar (`flag`) con un despachador de subcomandos propio. Sin cobra.
- **Configuración:** YAML (`forge.yaml`) con `gopkg.in/yaml.v3`, la única dependencia externa, tomada de la caché local de módulos (sin descargas). Validación sobre structs Go.
- **Logs:** `slog`, salida legible y modo JSON.
- **Git:** CLI `git` invocado con argv.
- **Build:** Docker CLI con argumentos separados (sin shell), comprobando errores y versiones.
- **Export:** `docker save` + gzip.
- **Transporte:** binarios `ssh` y `sftp` del sistema invocados con argv; `StrictHostKeyChecking` activo. No se expone nada a Internet.
- **Servidor remoto:** `deltactl` (repo `itc-deploy`). No hay agente de FORGE.
- **Estado local:** archivo JSON de releases en el proyecto (`.forge/`), sin base de datos.
- **Checksums:** SHA-256.
- **VPN:** WireGuard, administrada fuera de FORGE; FORGE verifica conectividad.

## 4. Comandos CLI propuestos

```text
forge init                 Crear forge.yaml de ejemplo
forge config validate      Validar configuración
forge build                Construir las imágenes declaradas
forge package              Exportar imágenes y generar manifest/checksum
forge transfer             Transferir artefacto al servidor
forge deploy               build + package + transfer + deltactl load/check/deploy
forge status               Estado de la instancia (deltactl status) y último release local
forge releases             Historial local de releases
forge rollback             deltactl rollback de la instancia (o de un servicio)
forge doctor               Diagnóstico de Docker, SSH, VPN, deltactl y configuración
forge version              Mostrar versión del CLI
```

Opcionales: `--dry-run`, `--config`, `--tag` (validado), `--verbose`, `--json`.

## 5. Configuración de proyecto

`forge.yaml` vive en la raíz del **proyecto del backend** (el que tiene el Dockerfile):

```yaml
version: 1

project:
  name: auth-backend

build:
  images:
    - name: backend
      service: s0              # clave del servicio en deltactl (s0 = auth-backend, s1 = gueo-back-end)
      context: .
      dockerfile: Dockerfile
      image: auth-backend      # debe coincidir con S0_IMAGE de la instancia
      target: production
  tests:
    - ["npm", "test"]          # argv, nunca una línea de shell

deployment:
  host: 10.20.0.2              # IP privada WireGuard
  user: forge-deploy
  ssh_port: 22
  known_hosts: ~/.ssh/known_hosts
  remote_dir: /srv/forge
  deltactl_path: /srv/itc-deploy/deltactl
  instance: colorado           # instancia de deltactl a actualizar

retention:
  releases: 5                  # artefactos locales y en staging; nunca imágenes cargadas
```

No incluir claves privadas, contraseñas ni tokens. Secretos mediante archivos protegidos o variables de entorno. Los secretos de cada instancia viven en el servidor (`instances/<inst>/`), gestionados por `deltactl`.

## 6. Formato de artefacto

Cada ejecución crea un release con identificador único basado en commit Git y timestamp.

```text
dist/
  <release-id>/
    images/
      backend.tar.gz
    manifest.json
    checksums.sha256
    metadata.json
```

`metadata.json`: proyecto, release ID, commit SHA, fecha UTC, versión de FORGE, imágenes (nombre, tag, servicio, checksum) e instancia destino.

El servidor valida los checksums con `sha256sum -c` antes de cargar cualquier imagen. Un artefacto incompleto no se carga.

## 7. Flujo de deploy

1. Cargar `forge.yaml` y validar configuración, herramientas y Docker daemon.
2. Obtener commit SHA y comprobar estado Git (bloquear o avisar por cambios sin commitear).
3. Generar release ID inmutable.
4. Construir imágenes con tag `<image>:<release-id>`.
5. Ejecutar los tests configurados (argv).
6. Exportar y comprimir imágenes; crear manifest y checksums.
7. Transferir al staging remoto por SSH/SFTP.
8. Validar checksums en el servidor (`sha256sum -c`).
9. `deltactl load <archivo>` por cada imagen.
10. `deltactl env <inst> set <S?_TAG> <release-id>`.
11. `deltactl check <inst> [servicio]`; si falla, se detiene sin cambiar nada más.
12. `deltactl deploy <inst> [servicio]`: levanta y espera a `healthy`.
13. Si `deploy` falla: `deltactl rollback <inst> [servicio] --yes` y se informa el resultado.
14. Registrar el release en el historial local y aplicar retención.

## 8. Ejecutor remoto (sustituye a `forge-agent`)

FORGE solo ejecuta por SSH una **lista cerrada** de comandos, construidos con argv:

- `sha256sum -c checksums.sha256` dentro del staging del release.
- `deltactl load | env set | check | deploy | rollback | status | runs | images | doctor`.

Reglas:
- Se valida cada argumento (instancia, servicio, variable, release-id) con listas de caracteres permitidos antes de enviarlo.
- Ninguna cadena se concatena en un shell remoto.
- Se rechazan rutas absolutas o con `..` provenientes del manifest.
- Verificación del host SSH siempre activa.
- Límites de tamaño del artefacto y comprobación de espacio libre antes de transferir.
- Los secretos nunca pasan por FORGE.

**Seguridad en el servidor (a documentar):** usuario dedicado con clave dedicada; si el usuario pertenece al grupo `docker` equivale a root, así que se documenta y se recomienda restringir el acceso a la interfaz VPN.

## 9. Rollback

Lo ejecuta `deltactl rollback`, que usa las etiquetas previas guardadas por `deploy`. FORGE:
1. Lanza `deltactl rollback <inst> [servicio] --yes`.
2. Comprueba el código de salida y consulta `deltactl status`.
3. Registra el evento en el historial local.

Limitaciones heredadas: exige que la imagen previa siga cargada (la retención de FORGE nunca borra imágenes del servidor) y **no deshace cambios en MongoDB** ni en otros datos.

## 10. Health checks

Los hace `deltactl deploy` (espera a `healthy`). FORGE no declara éxito si `deploy` termina con código distinto de cero; además consulta `deltactl status` al final y lo muestra.

## 11. Layout del repositorio FORGE

```text
forge/
├── cmd/forge/                 # Entrypoint CLI
├── internal/
│   ├── cli/                   # despachador de subcomandos
│   ├── config/
│   ├── gitmeta/
│   ├── release/               # release-id e historial local
│   ├── build/
│   ├── artifact/              # export, manifest, checksums
│   ├── transport/             # ssh/sftp
│   ├── remote/                # ejecutor de deltactl (lista cerrada)
│   ├── deploy/
│   ├── doctor/
│   ├── execx/                 # exec.CommandContext con argv
│   └── logging/
├── docs/
├── examples/
├── go.mod
├── Makefile
├── README.md
└── forge.yaml.example
```

## 12. Milestones

### M0 — Diseño y bootstrap
- Esquema `forge.yaml`, frontera con `itc-deploy`, release y manifiesto.
- Módulo Go, estructura, Makefile.

### M1 — CLI local
- `forge init`, `config validate`, `doctor` (local), `version`.
- Resolver root del proyecto y config; logs y errores estructurados.

**Exit criteria:** ejecutable desde cualquier subdirectorio del proyecto; encuentra y valida la configuración.

### M2 — Build
- `forge build` con tags por release-id, tests, logs y códigos de salida.

**Exit criteria:** build reproducible y errores visibles; sin shell.

### M3 — Packaging
- `docker save` + gzip, manifest y SHA-256. `forge package`.

**Exit criteria:** artefacto verificable con metadata completa.

### M4 — VPN/SSH transport
- `ssh`/`sftp` con `known_hosts` verificado, staging remoto, `sha256sum -c` remoto.
- `forge transfer` y comprobaciones de transporte en `forge doctor`.

**Exit criteria:** transferencia autenticada y checksum validado en el servidor.

### M5 — Ejecutor remoto de deltactl
- Lista cerrada de comandos y validación de argumentos.
- `load`, `env set`, `check`, `deploy` y `forge deploy` completo.

**Exit criteria:** deploy de extremo a extremo contra un servidor de pruebas.

### M6 — Rollback y registro
- Rollback automático ante fallo de `deploy` y `forge rollback`.
- `forge status` y `forge releases` (historial local) y retención local.

**Exit criteria:** provocar un fallo y volver a la versión anterior vía `deltactl rollback`.

### M7 — Proyecto piloto (posterior, con autorización)
- Una instancia real de `itc-deploy` (backend `s0` o `s1`) en ventana controlada.
- Documentar procedimiento y recuperación.

### M8 — CI server (opcional posterior)
- Servidor dedicado, workspace aislado, pipeline por webhook o Git hook.
- No mezclar secretos de producción con jobs de build.

## 13. Testing

- Unit tests: config, release-id, manifests, checksums, validación de rutas y argumentos, retención.
- El ejecutor remoto y Docker se prueban con **dobles de prueba** (como el `fake-docker` de `itc-deploy`); ningún test toca servidores ni Docker reales.
- Simular: servidor desconectado, falta de espacio, checksum inválido, build fallido, `check` fallido, `deploy` fallido, SSH rechazado y `rollback` fallido.
- Las pruebas contra Docker o servidores reales son manuales y requieren autorización.

## 14. Operación y observabilidad

- Logs con timestamps, niveles e ID de ejecución.
- Historial local de releases.
- Salida terminal amigable y JSON.
- Código de salida distinto de cero ante errores.
- `forge status`, `forge releases`, `forge doctor`.
- Si `deltactl runs` indica una operación interrumpida, `forge doctor` lo avisa; FORGE nunca ejecuta `runs resolve` por su cuenta.

## 15. Reglas que no se deben romper

1. No ejecutar comandos shell construidos concatenando strings; usar `exec.CommandContext` con argv.
2. No aceptar path arbitrario desde manifest/artefactos.
3. No desactivar verificación de host SSH.
4. No exponer nada a Internet; limitar a VPN.
5. No incluir secretos en logs, artefactos ni Git.
6. No borrar ni recrear volúmenes persistentes.
7. No usar el tag mutable `latest` como identidad de release.
8. No cargar artefactos sin checksum y metadata validados.
9. No declarar éxito si `deltactl deploy` falla.
10. No borrar imágenes del servidor (`docker image prune -a`): sin ellas no hay rollback.
11. No modificar `itc-deploy` desde este repo; los cambios que haga falta pedirle se tratan aparte.

## 16. Definición de FORGE 1.0 terminado

Desde la raíz de un proyecto configurado, `forge deploy`:
- valida la configuración,
- identifica el commit,
- ejecuta tests,
- construye las imágenes,
- empaqueta y verifica los artefactos,
- transfiere por VPN/SSH y valida el checksum en el servidor,
- carga, valida y despliega con `deltactl`,
- informa la salud según `deltactl`,
- y vuelve a la versión anterior con `deltactl rollback` si el despliegue falla.

---

## Próximo paso práctico

Implementar M0–M6 por ramas desde `develop`. No tocar el servidor de producción ni `itc-deploy` hasta revisar el diseño del ejecutor remoto y sus permisos.
