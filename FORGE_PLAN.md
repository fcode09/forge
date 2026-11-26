# FORGE — Local Go CI/CD Deployment Platform
## Plan de desarrollo v1.0

**Nombre:** FORGE  
**Tagline:** Build local. Ship securely. Deploy reliably.  
**Versión del plan:** 1.0  
**Lenguaje:** Go  
**Objetivo:** construir una herramienta de CI/CD self-hosted, sin servicios SaaS ni scripts Bash como lógica de despliegue. El comando se ejecuta desde la carpeta de un proyecto y construye, empaqueta, transfiere y despliega imágenes Docker en un servidor remoto a través de una VPN privada.

---

## 1. Visión

FORGE será una herramienta CLI en Go para automatizar el ciclo de despliegue de proyectos Docker/Compose.

Desde la raíz del proyecto:

```bash
forge build
forge deploy
forge release
forge rollback
```

La primera versión prioriza ejecución explícita desde la ubicación del proyecto (modo local), con un servidor remoto de producción y comunicación por VPN. Más adelante se puede añadir un servidor CI dedicado que reciba eventos de Git y ejecute pipelines automáticos.

**Principios:**
- Todo el código propio en Go.
- No usar Bash como orquestador ni lógica de deployment.
- No depender de GitLab Cloud, GitHub Actions, Docker Hub ni un registry SaaS.
- Aprovechar Docker Engine/BuildKit y Docker Compose, invocados desde Go mediante APIs o subprocesses controlados.
- Transferencia de artefactos por SSH/SCP sobre VPN.
- Configuración declarativa y validada.
- Versiones reproducibles, checksums, logs y rollback.
- No tocar volúmenes persistentes ni datos de bases de datos durante un despliegue normal.

## 2. Arquitectura objetivo

```text
Developer / project directory
        |
        | forge build / forge deploy
        v
FORGE CLI (Go)
  ├── Config loader + validator
  ├── Git metadata / versioning
  ├── Docker build
  ├── Image export (OCI archive or Docker archive)
  ├── SHA-256 manifest
  ├── SSH/VPN transport
  └── Deployment orchestration
        |
        | WireGuard network + SSH
        v
Production host (Ubuntu)
  ├── FORGE receiver/agent (Go)
  ├── Artifact validation
  ├── Docker image import
  ├── Compose deployment
  ├── Health checks
  ├── Release history
  └── Rollback
```

### Fase inicial (v1.0)
El CLI se ejecuta desde la máquina de desarrollo o el servidor CI, dentro del proyecto. El servidor de producción solo expone un endpoint de recepción/despliegue protegido por SSH sobre la VPN.

### Evolución (v1.x)
Un servidor CI dedicado puede clonar repositorios, ejecutar `forge build` y `forge deploy` automáticamente por webhook o Git hook. El mismo CLI y formato de artefactos se reutilizan.

## 3. Stack

- **Lenguaje:** Go (versión estable vigente al iniciar implementación).
- **CLI:** `cobra` (comandos, flags, ayuda).
- **Configuración:** YAML (`forge.yaml`), validación con esquema/structs Go.
- **Logs:** `slog`, salida legible y modo JSON.
- **Git:** CLI Git invocado de forma segura, o librería si los requisitos lo justifican.
- **Build:** Docker BuildKit / Docker Engine. En v1, invocar Docker CLI con argumentos separados (sin shell), comprobando errores y versiones. Evaluar Docker API después.
- **Export de imágenes:** `docker save` y compresión gzip, o OCI archive si se adopta un flujo compatible.
- **Transporte:** SSH con claves, SCP/SFTP o transferencia SSH stream. No exponer el receiver a Internet.
- **Servidor remoto:** agente Go, ejecutado como servicio systemd.
- **Runtime:** Docker Engine y Docker Compose plugin.
- **Estado:** SQLite (historial de releases y despliegues).
- **Checksums:** SHA-256.
- **VPN:** WireGuard, administrada fuera de FORGE; FORGE verifica conectividad/configuración, no configura VPN en la primera versión.

## 4. Comandos CLI propuestos

```text
forge init                 Crear forge.yaml de ejemplo
forge config validate      Validar configuración
forge build                Construir las imágenes declaradas
forge package              Exportar imágenes y generar manifest/checksum
forge transfer             Transferir artefacto al host remoto
forge deploy               Ejecutar build + package + transfer + deploy
forge status               Consultar estado del host y último release
forge releases             Mostrar historial
forge rollback             Volver al release anterior o a uno especificado
forge doctor               Diagnóstico de Docker, SSH, VPN y configuración
forge version              Mostrar versión del CLI
```

Opcionales:
- `--dry-run`: imprimir acciones sin ejecutarlas.
- `--project`, `--config`: seleccionar proyecto/configuración.
- `--tag`: override de versión (validado).
- `--verbose`: logs detallados.
- `--json`: salida apta para automatización.

## 5. Configuración de proyecto

Archivo `forge.yaml` en la raíz:

```yaml
version: 1

project:
  name: itc-deploy
  compose_file: docker-compose.yml
  working_dir: .

build:
  images:
    - name: backend
      context: .
      dockerfile: Dockerfile
      image: itc-backend
      target: production
    # Añadir frontend u otros servicios si tienen Dockerfile propio.

deployment:
  host: 10.20.0.2        # IP privada WireGuard del host de producción
  user: forge-deploy
  ssh_port: 22
  remote_dir: /srv/forge
  compose_project: itc-deploy
  compose_file: docker-compose.yml

healthcheck:
  timeout: 90s
  interval: 5s
  # Definir checks HTTP o checks de contenedor según el proyecto.

retention:
  releases: 5
```

No incluir claves privadas, contraseñas ni tokens en este archivo. Secretos mediante archivos protegidos localmente o variables de entorno; para v1, documentar una solución segura y no almacenar secretos en manifests.

## 6. Formato de artefacto

Cada ejecución crea un release con identificador único basado en Git commit y timestamp (evitar depender solo del tag mutable).

```text
dist/
  <release-id>/
    images/
      backend.tar.gz
      frontend.tar.gz
    manifest.json
    checksums.sha256
    compose.yaml
    metadata.json
```

`metadata.json` incluye:
- nombre de proyecto
- release ID
- commit SHA
- fecha UTC
- versión de FORGE
- lista de imágenes, tags y digest/checksum
- archivos Compose incluidos

El agente remoto valida checksums antes de importar cualquier imagen. Artefactos incompletos no se despliegan.

## 7. Flujo de build y deploy

1. Encontrar y cargar `forge.yaml` en el directorio actual.
2. Validar configuración, herramientas requeridas, Docker daemon y archivos.
3. Obtener commit SHA y comprobar estado Git; advertir o bloquear cambios no commitados según configuración.
4. Generar release ID inmutable.
5. Construir imágenes con tags inmutables: `<image>:<release-id>`.
6. Ejecutar tests configurados (v1: comandos de test configurables; sin shell eval, usar argv).
7. Exportar imágenes.
8. Comprimir y crear manifiesto/checksums.
9. Transferir a staging remoto mediante SSH/VPN.
10. Validar checksum y metadata remotamente.
11. Importar imágenes (`docker load` o método OCI elegido).
12. Preparar directorio de release y compose con tags exactos.
13. Ejecutar Compose con validación previa (`docker compose config`) y actualizar servicios.
14. Esperar healthchecks con timeout.
15. Si pasa: marcar release como activo y guardar historial.
16. Si falla: volver a compose/images del release anterior; comprobar salud del rollback y reportar resultado.
17. Aplicar retención de artefactos/releases, sin borrar volúmenes ni datos persistentes.

## 8. Agente remoto

El programa Go `forge-agent` se instala en producción y se ejecuta como servicio systemd.

Responsabilidades:
- Recibir solicitudes autenticadas.
- Limitar acceso al usuario de despliegue y a la VPN.
- Recibir artefactos en staging.
- Validar paths (prevenir path traversal), tamaño, checksum, metadata y releases.
- Aplicar despliegue mediante Docker/Compose.
- Mantener historial de releases y estado.
- Ejecutar healthchecks y rollback.
- Proporcionar status y logs.

**Seguridad v1:** SSH con claves dedicadas; no aceptar comandos arbitrarios; comandos remotos permitidos explícitamente; permisos mínimos; firewall restringido a la interfaz VPN; límites de tamaño y espacio; evitar extraer archivos tar sin validar rutas; registrar operaciones.

## 9. Rollback

Guardar por release:
- Compose file renderizado y validado.
- Tags/digests de imágenes.
- Metadata.
- Resultado de healthchecks.
- Hora y commit.

Rollback:
1. Seleccionar release conocido.
2. Validar que sus artefactos/imágenes siguen disponibles.
3. Restaurar configuración Compose.
4. Reaplicar Compose.
5. Verificar salud.
6. Registrar evento.

Rollback de aplicación **no** significa rollback de MongoDB ni de otros datos persistentes. Migraciones de DB deben tener estrategia compatible hacia atrás y backups aparte.

## 10. Health checks

La v1 soporta:
- Estado de contenedores.
- Health status Docker cuando se define `HEALTHCHECK`.
- HTTP/HTTPS a endpoints configurados, preferentemente por red privada.
- Timeout global y reintentos.
- Resultado claro en logs.

No declarar deploy exitoso únicamente porque `docker compose up` terminó con exit code 0.

## 11. Layout del repositorio FORGE

```text
forge/
├── cmd/forge/                 # Entrypoint CLI
├── cmd/forge-agent/           # Entrypoint remoto
├── internal/
│   ├── config/
│   ├── project/
│   ├── git/
│   ├── build/
│   ├── artifact/
│   ├── transport/
│   ├── deploy/
│   ├── health/
│   ├── release/
│   ├── rollback/
│   ├── agent/
│   ├── state/
│   └── logging/
├── pkg/                       # APIs reutilizables solo si necesario
├── configs/
├── docs/
├── examples/
│   └── itc-deploy/
├── go.mod
├── Makefile
├── README.md
└── forge.yaml.example
```

## 12. Milestones

### M0 — Diseño y seguridad
- Definir schema `forge.yaml`.
- Elegir mecanismo SSH/agent.
- Documentar threat model.
- Definir releases y manifiesto.

### M1 — CLI local
- Crear Go module.
- Implementar `forge init`, `config validate`, `doctor`, `version`.
- Resolver project root y config.
- Logs y errores estructurados.

**Exit criteria:** ejecutable desde cualquier subdirectorio del proyecto; encuentra configuración y valida.

### M2 — Build
- Construir imagen(es) configuradas.
- Tags por commit/release.
- Tests.
- Capturar logs/exit codes.
- Implementar `forge build`.

**Exit criteria:** build reproducible y errores visibles; sin shell scripts como orquestadores.

### M3 — Packaging
- Export de imágenes.
- Comprimir.
- Crear manifest y SHA-256.
- Implementar `forge package`.

**Exit criteria:** artefacto verificable, metadata completa.

### M4 — VPN/SSH transport
- Configurar conectividad VPN fuera de FORGE.
- SSH con key dedicada y known_hosts verificado.
- Transferencia al staging.
- Implementar `forge transfer` y `forge doctor`.

**Exit criteria:** transferencia autenticada y checksum validado.

### M5 — Agent y deploy
- Implementar `forge-agent`.
- Instalar servicio systemd.
- Validar Compose.
- Importar imágenes.
- Ejecutar deploy y healthcheck.
- Guardar release.

**Exit criteria:** deploy end-to-end en servidor de pruebas.

### M6 — Rollback
- Persistir release activo/anterior.
- Rollback por ID.
- Healthcheck del rollback.
- Retención de releases.

**Exit criteria:** provocar fallo de healthcheck y restaurar release anterior automáticamente.

### M7 — Proyecto piloto
- Integrar `itc-deploy`.
- Revisar Compose, imágenes, variables y volúmenes.
- Deploy primero en ventana controlada.
- Documentar procedimiento y recuperación.

### M8 — CI server (opcional posterior)
- Servidor dedicado.
- Repositorios bare o webhook privado.
- Worker que ejecute FORGE en workspace aislado.
- Pipeline automático al push.
- No mezclar secretos de producción con jobs de build.
- Aislar jobs (contenedores/usuario sin privilegios cuando sea posible).

## 13. Testing

- Unit tests para config, IDs, manifests, checksum, path validation y retención.
- Tests de integración con Docker.
- Tests de transporte en entorno VPN.
- Tests del agente con artefactos corruptos, paths maliciosos y releases inválidos.
- Tests de deploy/rollback en proyecto de prueba.
- Simular: host desconectado, falta de espacio, checksum inválido, build fallido, container unhealthy, SSH rechazado y rollback fallido.

## 14. Operación y observabilidad

- Logs con timestamps y niveles.
- ID de ejecución en todos los logs.
- Registro local de releases.
- Salida terminal amigable y JSON.
- Código de salida distinto de cero ante errores.
- `forge status`, `forge releases`, `forge doctor`.
- Logs remotos limitados y consultables.

## 15. Reglas que no se deben romper

1. No ejecutar comandos shell construidos concatenando strings; usar `exec.CommandContext` con argv.
2. No aceptar path arbitrario desde manifest/artefactos.
3. No desactivar verificación de host SSH.
4. No exponer agent a Internet; limitar a VPN.
5. No incluir secretos en logs, artefactos ni Git.
6. No borrar ni recrear volúmenes persistentes en deploy/rollback.
7. No usar tags mutable `latest` como única identidad de release.
8. No desplegar artefactos sin checksum y metadata validada.
9. No declarar éxito sin healthcheck.
10. Mantener el release previo disponible hasta que el nuevo sea healthy.

## 16. Definición de FORGE 1.0 terminado

FORGE 1.0 se considera listo cuando, desde la raíz de un proyecto configurado, el usuario puede ejecutar:

```bash
forge deploy
```

Y FORGE:
- valida la configuración,
- identifica el commit,
- ejecuta tests,
- construye las imágenes,
- empaqueta y verifica los artefactos,
- transfiere por VPN/SSH,
- despliega remotamente,
- valida salud,
- guarda release,
- y realiza rollback automático si el deploy falla.

Todo el flujo es implementado en Go, sin scripts Bash de deployment como lógica central.

---

## Próximo paso práctico

Comenzar M0/M1: crear el repositorio `forge`, definir `forge.yaml`, inicializar el módulo Go y construir la CLI con `forge init`, `forge config validate` y `forge doctor`. No modificar todavía el servidor de producción hasta que el diseño del agente y permisos esté revisado.
