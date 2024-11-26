export const requireAuth = (): string => {
    let credentialInput = localStorage.getItem("adminCredentials");
    while (credentialInput === null) {
        credentialInput = prompt("Admin credentials");
    }
    localStorage.setItem("adminCredentials", credentialInput);
    return credentialInput;
}
