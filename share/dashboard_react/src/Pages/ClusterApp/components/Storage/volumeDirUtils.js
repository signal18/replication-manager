const APP_MOUNT_VOLUME_DIR = "mnt";

const getVolumeDirTokens = (volumedir) =>
  typeof volumedir === "string" ? volumedir.split(/\s+/).filter(Boolean) : [];

const defaultSubdir = (volumedir) => getVolumeDirTokens(volumedir)[0] || "";

const defaultS3Subdir = (volumedir) => {
  const dirs = getVolumeDirTokens(volumedir);
  return dirs.includes(APP_MOUNT_VOLUME_DIR) ? APP_MOUNT_VOLUME_DIR : defaultSubdir(volumedir);
};

const matchVolumeDirToken = (path, volumedir, fallback = defaultSubdir) => {
  const dirs = getVolumeDirTokens(volumedir);
  const match = dirs.find((dir) => path === dir || path.startsWith(`${dir}/`));
  return match || fallback(volumedir);
};

const extractSubDir = (volumedir, baseDirToken) => {
  if (!baseDirToken) return volumedir;
  if (volumedir === baseDirToken) return "";
  if (volumedir.startsWith(`${baseDirToken}/`)) return volumedir.substring(baseDirToken.length + 1);
  return volumedir;
};

const buildVolumeDir = (baseDirToken, subDir, nameFallback, { preserveBareToken = false } = {}) => {
  const trimmed = typeof subDir === "string" ? subDir.trim() : "";
  if (!trimmed || trimmed === "/") {
    if (nameFallback) {
      return baseDirToken ? `${baseDirToken}/${nameFallback}` : nameFallback;
    }
    return preserveBareToken ? (baseDirToken || "") : "";
  }
  const cleanSub = trimmed.startsWith("/") ? trimmed.slice(1) : trimmed;
  return baseDirToken ? `${baseDirToken}/${cleanSub}` : cleanSub;
};

const normalizeSubPath = (value) => {
  const raw = typeof value === "string" ? value.trim() : "";
  if (!raw || raw === "/" || raw === ".") return "/";
  return raw.startsWith("/") ? raw : `/${raw}`;
};

const composeSourcePath = (srcbasepath, subpath) => {
  const normalizedSubPath = normalizeSubPath(subpath);
  if (!srcbasepath) {
    return normalizedSubPath === "/" ? "." : normalizedSubPath.slice(1);
  }
  if (normalizedSubPath === "/") return srcbasepath;
  return `${srcbasepath}${normalizedSubPath}`.replace("//", "/");
};

const getDisplaySubPath = (srcpath, srcbasepath) => {
  const sourcePath = typeof srcpath === "string" ? srcpath : "";
  const basePath = typeof srcbasepath === "string" ? srcbasepath : "";

  if (basePath && sourcePath && sourcePath.startsWith(basePath)) {
    const rel = sourcePath.slice(basePath.length);
    return normalizeSubPath(rel);
  }

  return normalizeSubPath(sourcePath);
};

export {
  APP_MOUNT_VOLUME_DIR,
  buildVolumeDir,
  composeSourcePath,
  defaultS3Subdir,
  defaultSubdir,
  extractSubDir,
  getDisplaySubPath,
  getVolumeDirTokens,
  matchVolumeDirToken,
  normalizeSubPath,
};
