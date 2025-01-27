import { Checkbox, Input, List, ListItem, VStack } from "@chakra-ui/react";
import { debounce } from "lodash";
import { useState, useEffect } from "react";

const GrantCheckList = ({ grantOptions, onChange, parentStyles, user }) => {
    const [allGroups, setAllGroups] = useState(false);
    const [grants, setGrants] = useState([]);
    const [searchTerm, setSearchTerm] = useState(""); // State to manage the search query

    const isShowGrant =  function(user, grant) {
        if (user?.roles?.['sysops']) {
            return true;
        }

        return user?.grants?.[grant?.grant];
    }

    // Group the grants by their prefix (category)
    const groupGrants = (grants) => {
        const groupMap = new Map();

        grants.forEach(grant => {
            const [prefix] = grant.grant.split('-');

            if (!groupMap.has(prefix)) {
                groupMap.set(prefix, []);
            }

            groupMap.get(prefix).push(grant);
        });

        let gg = Array.from(groupMap, ([group, grants]) => ({ group, grants, selected: grants.every(grant => grant.selected), show: true }));
        setAllGroups(gg.every(group => group.selected));
        return gg;
    };

    useEffect(() => {
        setGrants(groupGrants(grantOptions));
    }, [grantOptions]);

    // Function to handle the search input change
    const handleSearch = (e) => {
        setSearchTerm(e.target.value);
    };

    // Filter grants based on the search term
    const filteredGrants = grants.map(group => ({
        ...group,
        grants: group.grants.filter(grant =>
            isShowGrant(user,grant) && grant.grant.toLowerCase().includes(searchTerm.toLowerCase()) // Case-insensitive search
        ),
        show : group.group.toLowerCase().includes(searchTerm.toLowerCase())
    })).filter(group => group.grants.length > 0); // Only include groups that have matching grants

    // Debounced update handler for selected grants
    const handleUpdate = debounce((selected) => {
        onChange(selected); // Send the latest selected values to parent component
    }, 500);

    useEffect(() => {
        const selected = [
            ...grants.filter(group => group.selected).map(group => group.group),
            ...grants.filter(group => !group.selected).flatMap(group =>
                group.grants.filter(grant => grant.selected).map(grant => grant.grant)
            ),
        ];
        handleUpdate(selected); // Replace with actual save logic
    }, [grants]);

    // Handle group checkbox changes
    const handleAllChange = (isChecked) => {
        // Update individual grants based on the group selection
        const updatedGrants = grants.map(group => {
            return {
                ...group,
                grants: group.grants.map(grant => ({ ...grant, selected: isChecked })),
                selected: isChecked
            };
        });
        setGrants(updatedGrants);
        setAllGroups(isChecked)
    };

    // Handle group checkbox changes
    const handleGroupChange = (groupName, isChecked) => {
        // Update individual grants based on the group selection
        const updatedGrants = grants.map(group => {
            if (group.group === groupName) {
                return {
                    ...group,
                    grants: group.grants.map(grant => ({ ...grant, selected: isChecked })),
                    selected: isChecked
                };
            }
            return group;
        });

        setGrants(updatedGrants);
        setAllGroups(updatedGrants.every(group => group.selected))
    };

    // Handle individual grant checkbox changes
    const handleGrantChange = (groupName, grantName, isChecked) => {
        const updatedGrants = grants.map(group => {
            if (group.group === groupName) {
                const updatedGrant = group.grants.map(grant =>
                    grant.grant === grantName ? { ...grant, selected: isChecked } : grant
                );
                const allSelected = updatedGrant.every(grant => grant.selected);
                return { ...group, grants: updatedGrant, selected: allSelected };
            }
            return group;
        });

        setGrants(updatedGrants); // Correctly update the selected groups
        setAllGroups(updatedGrants.every(group => group.selected))
    };

    return (
        <VStack className={parentStyles.aclContainer}>
            {/* Search Input */}
            <Input
                id="searchAcl"
                type="search"
                onChange={handleSearch}
                placeholder="Search ACL"
            />

            {/* List of Grants */}
            <List className={parentStyles.aclList}>
                { searchTerm.length === 0 && <ListItem key="allGroup" className={parentStyles.aclListItem}>
                    <Checkbox
                        isChecked={allGroups}
                        isIndeterminate={filteredGrants.some(group => group.grants.some(grant => grant.selected)) && !filteredGrants.every(group => group.grants.every(grant => grant.selected))}
                        onChange={(e) => handleAllChange(e.target.checked)}
                    >
                        Select All
                    </Checkbox>
                </ListItem>}
                {filteredGrants.map((group) => (
                    <ListItem key={group.group} className={parentStyles.aclCategoryItem}>
                        <Checkbox
                            isChecked={group.selected}
                            isDisabled={!group.show}
                            isIndeterminate={group.grants.some(grant => grant.selected) && !group.grants.every(grant => grant.selected)}
                            onChange={(e) => handleGroupChange(group.group, e.target.checked)}
                        >
                            {group.group} {/* Group Name */}
                        </Checkbox>
                        <List pl={4} mt={2}>
                            {group.grants.map((grant) => (
                                <ListItem key={grant.grant} className={parentStyles.aclListItem}>
                                    <Checkbox
                                        key={grant.grant}
                                        isChecked={grant.selected}
                                        onChange={(e) => handleGrantChange(group.group, grant.grant, e.target.checked)}
                                    >
                                        {grant.grant} {/* Individual Grant */}
                                    </Checkbox>
                                </ListItem>
                            ))}
                        </List>
                    </ListItem>
                ))}
            </List>
        </VStack>
    );
};

export default GrantCheckList;
